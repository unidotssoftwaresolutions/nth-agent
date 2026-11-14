package builds

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"sort"
	"strings"
)

// Fault is why a build was not counted. Counted is none.
type Fault int

const (
	Counted Fault = iota
	NotAZip
	TooManyEntries
	RunsPastItsRecord
	// UnlistedEntry: a file's own header sits where the archive's list names nothing, or disagrees with what the list says of it.
	UnlistedEntry
	LeavesTheApp
	NamedTwice
	UnknownPacking
	TooLarge
	Unreadable
)

// Sentence is what a person reads about the fault: plain, and naming no setting. Those the farm's unpacker also says are its
// words exactly.
func (f Fault) Sentence() string {
	switch f {
	case NotAZip:
		return "it is not a zip archive"
	case TooManyEntries:
		return "it lists more than 50,000 files"
	case RunsPastItsRecord:
		return "its list of files does not end where the archive says it ends"
	case UnlistedEntry:
		return "it holds a file that its list of files does not name, or names differently"
	case LeavesTheApp:
		return "a file in it is named for a place outside the app (a name that starts at the top of a disk, a drive, a backslash or ..)"
	case NamedTwice:
		return "it names the same file twice"
	case UnknownPacking:
		return "a file in it is encrypted, or packed in a way no build is"
	case TooLarge:
		return "it unpacks to more than 1 GiB"
	case Unreadable:
		return "this agent could not read back the file it wrote"
	}
	return ""
}

// Count walks the build at path as a zip archive with the farm's own list rules for an unpack, and unpacks nothing: Counted,
// or the first rule it breaks. kind is its extension (".ipa" or ".apk"); an .apk may carry its signing block between its last
// file and its list, the one stretch of bytes there no list names (Android's APK Signature Scheme v2 and later put it there).
//
//   - The end record a reader takes: the last one whose comment runs exactly to the end, with no other end record after it.
//   - The list walked to the size that record gives, one more entry looked for past it, and no more than MaxEntries.
//   - Every name inside the app: none empty, none holding a NUL or a backslash, none starting at "/" or with a drive, no ".."
//     part — and no name twice, compared without case, a folder's trailing "/" left out.
//   - Only stored or deflated files, none encrypted, and at most MaxUnpackedBytes declared in all.
//   - No file the list does not name: every listed file's own header starts exactly where the one before it ends (its data,
//     and its descriptor when it has one), the first at the archive's first byte and the last ending where the list begins,
//     and says what the list says of it — its name, its packing, its checksum and its sizes.
//
// What is not done is what needs unpacking: the real bytes are never inflated here. The phone unpacks the build, not this
// computer; the count is what keeps an archive that lies about itself from reaching Appium at all.
func Count(path, kind string) Fault {
	file, err := os.Open(path)
	if err != nil {
		return Unreadable
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return Unreadable
	}
	return count(file, info.Size(), kind == ".apk")
}

type entry struct {
	name             string
	flags, method    uint16
	crc              uint32
	packed, declared int64
	localAt          int64
	zip64            bool
}

const (
	localSignature      = 0x04034b50
	listSignature       = 0x02014b50
	descriptorSignature = 0x08074b50
	endSignature        = 0x06054b50
)

// errShort is a read that ran past the archive: the archive is not what it says.
var errShort = errors.New("past the end")

func readAt(r io.ReaderAt, at int64, n int) ([]byte, error) {
	if at < 0 || n < 0 {
		return nil, errShort
	}
	b := make([]byte, n)
	if got, err := r.ReadAt(b, at); got != n {
		if err == nil || err == io.EOF {
			err = errShort
		}
		return nil, err
	}
	return b, nil
}

func u16(b []byte) uint16 { return binary.LittleEndian.Uint16(b) }
func u32(b []byte) uint32 { return binary.LittleEndian.Uint32(b) }
func u64(b []byte) uint64 { return binary.LittleEndian.Uint64(b) }

func count(r io.ReaderAt, length int64, apk bool) Fault {
	entries, listAt, fault := walk(r, length)
	if fault != Counted {
		return fault
	}

	// Names, packing and the declared total, before a byte of any file is read.
	names := map[string]bool{}
	var declared int64
	for _, e := range entries {
		if !inside(e.name) {
			return LeavesTheApp
		}
		key := strings.ToLower(strings.TrimRight(e.name, "/"))
		if names[key] {
			return NamedTwice
		}
		names[key] = true
		if e.flags&1 != 0 || (e.method != 0 && e.method != 8) {
			return UnknownPacking
		}
		if e.declared < 0 || e.packed < 0 {
			return NotAZip
		}
		declared += e.declared
		if declared > MaxUnpackedBytes {
			return TooLarge
		}
	}

	// Every listed file's own header, in the order they lie: each starts where the one before ends.
	order := make([]int, len(entries))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool { return entries[order[a]].localAt < entries[order[b]].localAt })
	var expected int64
	for _, i := range order {
		e := entries[i]
		if e.localAt != expected {
			return UnlistedEntry
		}
		at, descriptor, ok := localData(r, length, e)
		if !ok {
			return UnlistedEntry
		}
		expected = at + e.packed + descriptor
	}
	if expected != listAt && !(apk && signingBlock(r, expected, listAt)) {
		return UnlistedEntry
	}
	return Counted
}

// inside says whether a file named name stays inside the app it belongs to.
func inside(name string) bool {
	if name == "" || strings.ContainsAny(name, "\x00\\") || strings.HasPrefix(name, "/") || (len(name) > 1 && name[1] == ':') {
		return false
	}
	for _, part := range strings.Split(name, "/") {
		if part == ".." {
			return false
		}
	}
	return true
}

// signingBlock says whether the bytes from at to listAt are exactly one APK signing block: its size, its pairs, its size
// again, and its magic — and do not start like a file's own header, which a reader walking headers in order would take.
func signingBlock(r io.ReaderAt, at, listAt int64) bool {
	const magic = "APK Sig Block 42"
	if listAt-at < 8+8+16 {
		return false
	}
	tail, err := readAt(r, listAt-24, 24)
	if err != nil || string(tail[8:]) != magic {
		return false
	}
	head, err := readAt(r, at, 8)
	if err != nil || u32(head) == localSignature {
		return false
	}
	size := u64(head)
	return size == u64(tail) && size >= 24 && size == uint64(listAt-at-8)
}

// walk reads the archive's list: the end record, ZIP64's when the record says so, and every list entry.
func walk(r io.ReaderAt, length int64) ([]entry, int64, Fault) {
	if length < 22 {
		return nil, 0, NotAZip
	}
	tailLength := int64(22 + 65_535)
	if length < tailLength {
		tailLength = length
	}
	tail, err := readAt(r, length-tailLength, int(tailLength))
	if err != nil {
		return nil, 0, NotAZip
	}
	end := -1
	for i := len(tail) - 22; i >= 0; i-- {
		if u32(tail[i:]) != endSignature {
			continue
		}
		if i+22+int(u16(tail[i+20:])) != len(tail) {
			continue
		}
		end = i
		break
	}
	if end < 0 {
		return nil, 0, NotAZip
	}
	if bytes.Contains(tail[end+4:], []byte{0x50, 0x4b, 0x05, 0x06}) {
		return nil, 0, RunsPastItsRecord
	}
	endAt := length - tailLength + int64(end)

	count := int64(u16(tail[end+10:]))
	listSize := int64(u32(tail[end+12:]))
	listAt := int64(u32(tail[end+16:]))
	if count == 0xFFFF || listSize == 0xFFFFFFFF || listAt == 0xFFFFFFFF {
		if endAt < 20 {
			return nil, 0, NotAZip
		}
		locator, err := readAt(r, endAt-20, 20)
		if err != nil || u32(locator) != 0x07064b50 {
			return nil, 0, NotAZip
		}
		zip64At := int64(u64(locator[8:]))
		if zip64At < 0 || zip64At > endAt-20-56 {
			return nil, 0, NotAZip
		}
		record, err := readAt(r, zip64At, 56)
		if err != nil || u32(record) != 0x06064b50 {
			return nil, 0, NotAZip
		}
		count, listSize, listAt = int64(u64(record[32:])), int64(u64(record[40:])), int64(u64(record[48:]))
	}
	if count < 0 || count > MaxEntries {
		return nil, 0, TooManyEntries
	}
	if listAt < 0 || listSize < 0 || listAt > length || listSize > length-listAt {
		return nil, 0, NotAZip
	}

	var entries []entry
	var walked int64
	for walked < listSize {
		if listSize-walked < 46 {
			return nil, 0, NotAZip
		}
		header, err := readAt(r, listAt+walked, 46)
		if err != nil || u32(header) != listSignature {
			return nil, 0, NotAZip
		}
		nameLength := int(u16(header[28:]))
		variable := nameLength + int(u16(header[30:])) + int(u16(header[32:]))
		rest, err := readAt(r, listAt+walked+46, variable)
		if err != nil {
			return nil, 0, NotAZip
		}
		walked += 46 + int64(variable)
		if walked > listSize {
			return nil, 0, NotAZip
		}
		if len(entries)+1 > MaxEntries {
			return nil, 0, TooManyEntries
		}
		entries = append(entries, entryOf(header, rest, nameLength))
	}
	if int64(len(entries)) != count {
		return nil, 0, RunsPastItsRecord
	}
	if listAt+listSize <= length-4 {
		if next, err := readAt(r, listAt+listSize, 4); err == nil && u32(next) == listSignature {
			return nil, 0, RunsPastItsRecord
		}
	}
	return entries, listAt, Counted
}

func entryOf(header, rest []byte, nameLength int) entry {
	packed := int64(u32(header[20:]))
	declared := int64(u32(header[24:]))
	at := int64(u32(header[42:]))
	zip64 := false
	extra := rest[nameLength : nameLength+int(u16(header[30:]))]
	for len(extra) >= 4 {
		id, size := u16(extra), int(u16(extra[2:]))
		field := extra[4:min(len(extra), 4+size)]
		if id == 0x0001 {
			zip64 = true
			if declared == 0xFFFFFFFF && len(field) >= 8 {
				declared, field = int64(u64(field)), field[8:]
			}
			if packed == 0xFFFFFFFF && len(field) >= 8 {
				packed, field = int64(u64(field)), field[8:]
			}
			if at == 0xFFFFFFFF && len(field) >= 8 {
				at = int64(u64(field))
			}
			break
		}
		extra = extra[min(len(extra), 4+size):]
	}
	return entry{name: string(rest[:nameLength]), flags: u16(header[8:]), method: u16(header[10:]), crc: u32(header[16:]),
		packed: packed, declared: declared, localAt: at, zip64: zip64}
}

// localData is where a listed file's data starts and how many bytes of descriptor follow it — or false when its own header
// is not one, or says another name, packing, checksum or size than the list.
func localData(r io.ReaderAt, length int64, e entry) (int64, int64, bool) {
	if e.localAt < 0 || e.localAt > length-30 {
		return 0, 0, false
	}
	local, err := readAt(r, e.localAt, 30)
	if err != nil || u32(local) != localSignature {
		return 0, 0, false
	}
	flags := u16(local[6:])
	if u16(local[8:]) != e.method || flags&8 != e.flags&8 {
		return 0, 0, false
	}
	nameLength, extraLength := int64(u16(local[26:])), int64(u16(local[28:]))
	at := e.localAt + 30 + nameLength + extraLength
	if at+e.packed > length {
		return 0, 0, false
	}
	name, err := readAt(r, e.localAt+30, int(nameLength))
	if err != nil || string(name) != e.name {
		return 0, 0, false
	}

	if flags&8 == 0 {
		// Its own header carries its sizes: they must be the list's (0xFFFFFFFF when they are in its ZIP64 field).
		packed, declared := int64(u32(local[18:])), int64(u32(local[22:]))
		if u32(local[14:]) != e.crc {
			return 0, 0, false
		}
		if (packed != 0xFFFFFFFF && packed != e.packed) || (declared != 0xFFFFFFFF && declared != e.declared) {
			return 0, 0, false
		}
		return at, 0, true
	}

	// Its sizes follow its data, in a descriptor: an optional signature, the checksum, and the two sizes (eight bytes each in
	// ZIP64).
	descriptor := make([]byte, 24)
	read, _ := r.ReadAt(descriptor, at+e.packed)
	signed := 0
	if read >= 4 && u32(descriptor) == descriptorSignature {
		signed = 4
	}
	sizes := 8
	if e.zip64 {
		sizes = 16
	}
	if read < signed+4+sizes {
		return 0, 0, false
	}
	fields := descriptor[signed:]
	if u32(fields) != e.crc {
		return 0, 0, false
	}
	var packed, declared int64
	if e.zip64 {
		packed, declared = int64(u64(fields[4:])), int64(u64(fields[12:]))
	} else {
		packed, declared = int64(u32(fields[4:])), int64(u32(fields[8:]))
	}
	if packed != e.packed || declared != e.declared {
		return 0, 0, false
	}
	return at, int64(signed + 4 + sizes), true
}
