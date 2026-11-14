package builds

import (
	"compress/flate"
	"hash/crc32"
	"io"
)

// WHAT A BUILD REALLY HOLDS (WHAT_A_LEASE_CAN_ASK). The list's sizes are what an archive says of itself; an unpacker on this
// computer — a driver reading the build's Info.plist or AndroidManifest.xml, say — gets the bytes. So each file is unpacked
// on the way, into nothing: never written, counted, and checked. It must unpack to exactly the size the list declares, with
// the list's checksum, the way the farm's own unpacker holds each file it writes; one byte more than declared is never read.
// The declared total is already held to MaxUnpackedBytes, so the real one is too.

// holdsWhatItSays says whether e's data, which starts at at, unpacks to exactly what the list says: e.declared bytes whose
// checksum is e.crc. A stored file's bytes are its data, so it must also be exactly as long as it declares.
func holdsWhatItSays(r io.ReaderAt, at int64, e entry) bool {
	data := io.NewSectionReader(r, at, e.packed)
	var unpacked io.Reader = data
	switch e.method {
	case 0:
		if e.packed != e.declared {
			return false
		}
	case 8:
		inflater := flate.NewReader(data)
		defer inflater.Close()
		unpacked = inflater
	default:
		return false
	}
	sum := crc32.NewIEEE()
	n, err := io.Copy(sum, io.LimitReader(unpacked, e.declared+1))
	if err != nil || n != e.declared || sum.Sum32() != e.crc {
		return false
	}
	return true
}
