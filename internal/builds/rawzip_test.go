package builds

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"hash/crc32"
)

// A ZIP WRITER THAT CAN LIE, for the seals: every field of every record is written from what the test says, so an archive
// can disagree with itself in exactly the one way a rule is about. archive/zip cannot write one: it keeps its records true.

// rawEntry is one file of a crafted archive. The zero value of every lie is the truth.
type rawEntry struct {
	name    string
	data    []byte
	deflate bool
	flags   uint16 // added to the flags written (1: encrypted; 8 is set by descriptor)
	method  int    // when > 0, the packing written instead of the real one
	// descriptor writes the sizes and checksum after the data (flag 8), as a streaming writer does.
	descriptor bool
	unixMode   uint32 // when > 0, the entry says it was made on a Unix machine, with this mode

	// Lies.
	localName     string // the file's own header names this instead
	localDeclared int64  // when > 0, the file's own header declares this many unpacked bytes
	localCRC      uint32 // when > 0, the file's own header carries this checksum
	descDeclared  int64  // when > 0, the descriptor declares this many unpacked bytes
	declared      int64  // when > 0, both headers declare this many unpacked bytes
	localAtShift  int64  // added to where the list says the file's own header is
	before        []byte // written just before this file's own header, and named by no list entry
}

// rawZip is a crafted archive.
type rawZip struct {
	entries []rawEntry
	// beforeList is written after the last file and before the list.
	beforeList []byte
	// apkSigningBlock, when set, is written just before the list as an APK signing block holding these bytes.
	apkSigningBlock []byte
	// apkBlockSizeLie is added to the size the signing block's first field gives.
	apkBlockSizeLie int64
	// count, when >= 0 and countSet, is the number of files the end record gives.
	count    int
	countSet bool
	// listSizeLie is added to the list's size in the end record.
	listSizeLie int64
	// extraListEntry writes one more list entry after the list the end record describes.
	extraListEntry bool
	comment        []byte
	// zip64 writes the ZIP64 end record and its locator, with the end record's fields set to "see ZIP64".
	zip64 bool
	// zip64LocatorPast points the locator past the end of the archive.
	zip64LocatorPast bool
}

func le16(b *bytes.Buffer, v uint16) { binary.Write(b, binary.LittleEndian, v) }
func le32(b *bytes.Buffer, v uint32) { binary.Write(b, binary.LittleEndian, v) }
func le64(b *bytes.Buffer, v uint64) { binary.Write(b, binary.LittleEndian, v) }

func (z rawZip) bytes() []byte {
	var out bytes.Buffer
	type listed struct {
		e                rawEntry
		packed, declared int64
		crc              uint32
		method, flags    uint16
		at               int64
	}
	var list []listed
	for _, e := range z.entries {
		out.Write(e.before)
		packedData := e.data
		method := uint16(0)
		if e.deflate {
			var p bytes.Buffer
			w, _ := flate.NewWriter(&p, flate.DefaultCompression)
			w.Write(e.data)
			w.Close()
			packedData = p.Bytes()
			method = 8
		}
		if e.method > 0 {
			method = uint16(e.method)
		}
		flags := e.flags
		if e.descriptor {
			flags |= 8
		}
		declared := int64(len(e.data))
		if e.declared > 0 {
			declared = e.declared
		}
		crc := crc32.ChecksumIEEE(e.data)
		l := listed{e: e, packed: int64(len(packedData)), declared: declared, crc: crc, method: method, flags: flags, at: int64(out.Len())}
		list = append(list, l)

		name := e.name
		if e.localName != "" {
			name = e.localName
		}
		localCRC, localDeclared := crc, declared
		if e.localCRC > 0 {
			localCRC = e.localCRC
		}
		if e.localDeclared > 0 {
			localDeclared = e.localDeclared
		}
		le32(&out, 0x04034b50)
		le16(&out, 20)
		le16(&out, flags)
		le16(&out, method)
		le16(&out, 0)
		le16(&out, 0)
		if e.descriptor {
			le32(&out, 0)
			le32(&out, 0)
			le32(&out, 0)
		} else {
			le32(&out, localCRC)
			le32(&out, uint32(l.packed))
			le32(&out, uint32(localDeclared))
		}
		le16(&out, uint16(len(name)))
		le16(&out, 0)
		out.WriteString(name)
		out.Write(packedData)
		if e.descriptor {
			descDeclared := declared
			if e.descDeclared > 0 {
				descDeclared = e.descDeclared
			}
			le32(&out, 0x08074b50)
			le32(&out, crc)
			le32(&out, uint32(l.packed))
			le32(&out, uint32(descDeclared))
		}
	}
	out.Write(z.beforeList)
	if z.apkSigningBlock != nil {
		// size (of what follows it), the id-value pairs, size again, the magic.
		size := uint64(len(z.apkSigningBlock) + 8 + 16)
		le64(&out, uint64(int64(size)+z.apkBlockSizeLie))
		out.Write(z.apkSigningBlock)
		le64(&out, size)
		out.WriteString("APK Sig Block 42")
	}
	listAt := int64(out.Len())
	writeEntry := func(l listed) {
		le32(&out, 0x02014b50)
		made := uint16(20)
		external := uint32(0)
		if l.e.unixMode > 0 {
			made |= 3 << 8
			external = l.e.unixMode << 16
		}
		le16(&out, made)
		le16(&out, 20)
		le16(&out, l.flags)
		le16(&out, l.method)
		le16(&out, 0)
		le16(&out, 0)
		le32(&out, l.crc)
		le32(&out, uint32(l.packed))
		le32(&out, uint32(l.declared))
		le16(&out, uint16(len(l.e.name)))
		le16(&out, 0)
		le16(&out, 0)
		le16(&out, 0)
		le16(&out, 0)
		le32(&out, external)
		le32(&out, uint32(l.at+l.e.localAtShift))
		out.WriteString(l.e.name)
	}
	for _, l := range list {
		writeEntry(l)
	}
	listSize := int64(out.Len()) - listAt
	if z.extraListEntry && len(list) > 0 {
		writeEntry(list[0])
	}
	count := len(list)
	if z.countSet {
		count = z.count
	}
	if z.zip64 {
		recordAt := int64(out.Len())
		le32(&out, 0x06064b50)
		le64(&out, 44)
		le16(&out, 45)
		le16(&out, 45)
		le32(&out, 0)
		le32(&out, 0)
		le64(&out, uint64(count))
		le64(&out, uint64(count))
		le64(&out, uint64(listSize+z.listSizeLie))
		le64(&out, uint64(listAt))
		le32(&out, 0x07064b50)
		le32(&out, 0)
		if z.zip64LocatorPast {
			le64(&out, uint64(recordAt+1<<20))
		} else {
			le64(&out, uint64(recordAt))
		}
		le32(&out, 1)
	}
	le32(&out, 0x06054b50)
	le16(&out, 0)
	le16(&out, 0)
	if z.zip64 {
		le16(&out, 0xFFFF)
		le16(&out, 0xFFFF)
		le32(&out, 0xFFFFFFFF)
		le32(&out, 0xFFFFFFFF)
	} else {
		le16(&out, uint16(count))
		le16(&out, uint16(count))
		le32(&out, uint32(listSize+z.listSizeLie))
		le32(&out, uint32(listAt))
	}
	le16(&out, uint16(len(z.comment)))
	out.Write(z.comment)
	return out.Bytes()
}

// anApp is the shape of a small real build: a folder, an executable made on a Unix machine, a packed plist and a stored file.
func anApp() []rawEntry {
	return []rawEntry{
		{name: "Payload/", unixMode: 0o40755},
		{name: "Payload/Lantern.app/Lantern", data: bytes.Repeat([]byte{0xCF, 0xFA, 0xED, 0xFE}, 300), unixMode: 0o100755},
		{name: "Payload/Lantern.app/Info.plist", data: []byte(`<?xml version="1.0"?><plist><dict><key>CFBundleIdentifier</key><string>com.example.lantern</string></dict></plist>`), deflate: true, descriptor: true},
		{name: "Payload/Lantern.app/Assets.car", data: bytes.Repeat([]byte("asset "), 200)},
	}
}
