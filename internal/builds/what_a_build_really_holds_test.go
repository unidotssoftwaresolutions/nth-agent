package builds

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"hash/crc32"
	"strings"
	"testing"
)

// WHAT A BUILD REALLY HOLDS (WHAT_A_LEASE_CAN_ASK). The count read only an archive's list: a file that declared a kilobyte
// and unpacked to a megabyte passed, and so did a stored file whose bytes were not the size it declared. Each file is
// now unpacked into nothing on the way — counted and checked, never written — and must hold exactly what the list says,
// checksum and all, the way the farm's own unpacker holds it when it writes one out.

func TestAFileThatUnpacksToOtherThanItDeclaresIsRefused(t *testing.T) {
	bigger := bytes.Repeat([]byte("A"), 1<<20) // a megabyte of one letter packs into a few kilobytes
	for name, c := range map[string]struct {
		archive []byte
		kind    string
	}{
		"a packed file that unpacks to more than it declares": {rawZip{entries: append(anApp(),
			rawEntry{name: "Payload/Lantern.app/big.bin", data: bigger, deflate: true, declared: 1024})}.bytes(), ".ipa"},
		"a packed file that unpacks to less than it declares": {rawZip{entries: append(anApp(),
			rawEntry{name: "Payload/Lantern.app/small.bin", data: []byte("tiny"), deflate: true, declared: 1 << 20})}.bytes(), ".ipa"},
		"a stored file whose bytes are not the size it declares": {rawZip{entries: append(anApp(),
			rawEntry{name: "Payload/Lantern.app/stored.bin", data: bytes.Repeat([]byte{9}, 4096), declared: 1})}.bytes(), ".ipa"},
		"a packed file, with its sizes after it": {rawZip{entries: append(anApp(),
			rawEntry{name: "Payload/Lantern.app/streamed.bin", data: bigger, deflate: true, descriptor: true, declared: 2048})}.bytes(), ".ipa"},
	} {
		t.Run(name, func(t *testing.T) {
			got := countBytes(t, c.archive, c.kind)
			if got == Counted {
				t.Fatalf("an archive whose file unpacks to other than it declares was counted")
			}
			if !strings.Contains(got.Sentence(), "unpacks to other than the archive says it holds") {
				t.Fatalf("got %v (%q): the sentence is the farm's own words for it", got, got.Sentence())
			}
		})
	}
	// …and a build that holds what it says still counts, packed or stored, with its sizes before its data or after it.
	if got := countBytes(t, rawZip{entries: append(anApp(), rawEntry{name: "Payload/Lantern.app/big.bin", data: bigger, deflate: true},
		rawEntry{name: "Payload/Lantern.app/streamed.bin", data: bigger, deflate: true, descriptor: true})}.bytes(), ".ipa"); got != Counted {
		t.Fatalf("a build that holds what it says must count: %v (%q)", got, got.Sentence())
	}
}

// A ZIP64 size near 2^63 added to the running total wrapped it below zero, and every later comparison passed. A file that
// declares more than is left of the bound is refused before it is added.
func TestADeclaredTotalThatWouldWrapIsRefused(t *testing.T) {
	archive := wrappingArchive(t)
	if got := count(bytes.NewReader(archive), int64(len(archive)), false); got != TooLarge {
		t.Fatalf("an archive whose second file declares 2^63-1 bytes: got %v (%q), want %v", got, got.Sentence(), TooLarge)
	}
}

// wrappingArchive is two files written from the first byte with nothing between them and the list: "a", stored, one byte;
// and "b", packed, whose list and own header give their sizes as 0xFFFFFFFF and whose ZIP64 field in the list declares
// 2^63-1 unpacked bytes and its real packed size.
func wrappingArchive(t *testing.T) []byte {
	t.Helper()
	var out, list bytes.Buffer
	le := func(b *bytes.Buffer, v any) { _ = binary.Write(b, binary.LittleEndian, v) }

	// "a": stored, one byte.
	aAt := out.Len()
	le(&out, uint32(localSignature))
	le(&out, uint16(20))
	le(&out, uint16(0))
	le(&out, uint16(0))
	le(&out, uint32(0))
	le(&out, crc32.ChecksumIEEE([]byte("x")))
	le(&out, uint32(1))
	le(&out, uint32(1))
	le(&out, uint16(1))
	le(&out, uint16(0))
	out.WriteString("a")
	out.WriteString("x")

	// "b": packed, sizes in ZIP64.
	var packed bytes.Buffer
	w, err := flate.NewWriter(&packed, flate.BestCompression)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write([]byte("bb"))
	_ = w.Close()
	crc := crc32.ChecksumIEEE([]byte("bb"))
	bAt := out.Len()
	le(&out, uint32(localSignature))
	le(&out, uint16(45))
	le(&out, uint16(0))
	le(&out, uint16(8))
	le(&out, uint32(0))
	le(&out, crc)
	le(&out, uint32(0xFFFFFFFF))
	le(&out, uint32(0xFFFFFFFF))
	le(&out, uint16(1))
	le(&out, uint16(0))
	out.WriteString("b")
	out.Write(packed.Bytes())

	listAt := out.Len()
	central := func(name string, method uint16, crc uint32, packedSize, declared uint32, at int, extra []byte) {
		le(&list, uint32(listSignature))
		le(&list, uint16(45))
		le(&list, uint16(45))
		le(&list, uint16(0))
		le(&list, method)
		le(&list, uint32(0))
		le(&list, crc)
		le(&list, packedSize)
		le(&list, declared)
		le(&list, uint16(len(name)))
		le(&list, uint16(len(extra)))
		le(&list, uint16(0))
		le(&list, uint16(0))
		le(&list, uint16(0))
		le(&list, uint32(0))
		le(&list, uint32(at))
		list.WriteString(name)
		list.Write(extra)
	}
	central("a", 0, crc32.ChecksumIEEE([]byte("x")), 1, 1, aAt, nil)
	var extra bytes.Buffer
	le(&extra, uint16(0x0001))
	le(&extra, uint16(16))
	le(&extra, uint64(1<<63-1))
	le(&extra, uint64(packed.Len()))
	central("b", 8, crc, 0xFFFFFFFF, 0xFFFFFFFF, bAt, extra.Bytes())
	out.Write(list.Bytes())

	le(&out, uint32(endSignature))
	le(&out, uint16(0))
	le(&out, uint16(0))
	le(&out, uint16(2))
	le(&out, uint16(2))
	le(&out, uint32(list.Len()))
	le(&out, uint32(listAt))
	le(&out, uint16(0))
	return out.Bytes()
}
