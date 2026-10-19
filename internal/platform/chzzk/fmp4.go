package chzzk

import (
	"cmp"
	"encoding/binary"
	"fmt"
	"slices"

	"github.com/manwol-ryu/momentforge/internal/platform"
)

// Just enough ISO BMFF to join fragmented-MP4 HLS segments into one file
// that starts at 0 s: read each track's timescale from the init section,
// move every fragment's decode time (tfdt) back by the first segment's
// start, and drop the per-segment boxes that carry no media (styp, sidx,
// emsg, prft). Each movie fragment is also renumbered: CHZZK repeats the
// fragment sequence number inside a segment (and every segment restarts
// it), and AVFoundation stops reading a joined file at the first repeat.
// Samples are copied as they are; nothing is re-encoded.

type box struct {
	typ        string
	start, end int // the whole box in its buffer
	body       int // offset of the payload
}

// boxes lists the boxes in b[from:to].
func boxes(b []byte, from, to int) ([]box, error) {
	var out []box
	for off := from; off < to; {
		if to-off < 8 {
			return nil, fmt.Errorf("truncated box header at %d", off)
		}
		size := uint64(binary.BigEndian.Uint32(b[off:]))
		typ := string(b[off+4 : off+8])
		hdr := 8
		switch size {
		case 0:
			size = uint64(to - off)
		case 1:
			if to-off < 16 {
				return nil, fmt.Errorf("truncated large box at %d", off)
			}
			size, hdr = binary.BigEndian.Uint64(b[off+8:]), 16
		}
		if size < uint64(hdr) || size > uint64(to-off) {
			return nil, fmt.Errorf("box %q at %d has size %d", typ, off, size)
		}
		out = append(out, box{typ: typ, start: off, end: off + int(size), body: off + hdr})
		off += int(size)
	}
	return out, nil
}

func child(b []byte, parent box, typ string) (box, bool, error) {
	list, err := boxes(b, parent.body, parent.end)
	if err != nil {
		return box{}, false, err
	}
	for _, c := range list {
		if c.typ == typ {
			return c, true, nil
		}
	}
	return box{}, false, nil
}

func children(b []byte, parent box, typ string) ([]box, error) {
	list, err := boxes(b, parent.body, parent.end)
	if err != nil {
		return nil, err
	}
	var out []box
	for _, c := range list {
		if c.typ == typ {
			out = append(out, c)
		}
	}
	return out, nil
}

// initTimescales reads track ID → timescale from an init section (ftyp + moov).
func initTimescales(b []byte) (map[uint32]uint32, error) {
	top, err := boxes(b, 0, len(b))
	if err != nil {
		return nil, err
	}
	out := map[uint32]uint32{}
	for _, m := range top {
		if m.typ != "moov" {
			continue
		}
		traks, err := children(b, m, "trak")
		if err != nil {
			return nil, err
		}
		for _, t := range traks {
			tkhd, ok1, err := child(b, t, "tkhd")
			if err != nil {
				return nil, err
			}
			mdia, ok2, err := child(b, t, "mdia")
			if err != nil {
				return nil, err
			}
			if !ok1 || !ok2 {
				return nil, fmt.Errorf("track without tkhd or mdia")
			}
			mdhd, ok, err := child(b, mdia, "mdhd")
			if err != nil || !ok {
				return nil, fmt.Errorf("track without mdhd")
			}
			id, err := tkhdTrackID(b, tkhd)
			if err != nil {
				return nil, err
			}
			ts, err := mdhdTimescale(b, mdhd)
			if err != nil {
				return nil, err
			}
			out[id] = ts
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("init section has no tracks")
	}
	return out, nil
}

func tkhdTrackID(b []byte, x box) (uint32, error) {
	// version(1) flags(3), then creation/modification (4 or 8 each), then track_ID.
	off := x.body + 4 + 8
	if b[x.body] == 1 {
		off = x.body + 4 + 16
	}
	if off+4 > x.end {
		return 0, fmt.Errorf("short tkhd")
	}
	return binary.BigEndian.Uint32(b[off:]), nil
}

func mdhdTimescale(b []byte, x box) (uint32, error) {
	off := x.body + 4 + 8
	if b[x.body] == 1 {
		off = x.body + 4 + 16
	}
	if off+4 > x.end {
		return 0, fmt.Errorf("short mdhd")
	}
	ts := binary.BigEndian.Uint32(b[off:])
	if ts == 0 {
		return 0, fmt.Errorf("zero timescale")
	}
	return ts, nil
}

// fragment is one traf's track and the offset of its tfdt time field.
type fragment struct {
	track   uint32
	timeOff int // offset of baseMediaDecodeTime in the buffer
	wide    bool
}

// fragments finds every track fragment's decode time in a media segment.
func fragments(b []byte) ([]fragment, error) {
	top, err := boxes(b, 0, len(b))
	if err != nil {
		return nil, err
	}
	var out []fragment
	for _, m := range top {
		if m.typ != "moof" {
			continue
		}
		trafs, err := children(b, m, "traf")
		if err != nil {
			return nil, err
		}
		for _, t := range trafs {
			tfhd, ok1, err := child(b, t, "tfhd")
			if err != nil {
				return nil, err
			}
			tfdt, ok2, err := child(b, t, "tfdt")
			if err != nil {
				return nil, err
			}
			if !ok1 || !ok2 || tfhd.end-tfhd.body < 8 || tfdt.end-tfdt.body < 8 {
				return nil, fmt.Errorf("track fragment without tfhd or tfdt")
			}
			if flags := binary.BigEndian.Uint32(b[tfhd.body:]) & 0xffffff; flags&0x1 != 0 {
				// An absolute base data offset would point into the wrong place once segments are joined.
				return nil, fmt.Errorf("track fragment uses an absolute base data offset")
			}
			f := fragment{track: binary.BigEndian.Uint32(b[tfhd.body+4:]), timeOff: tfdt.body + 4, wide: b[tfdt.body] == 1}
			if f.wide && tfdt.end-tfdt.body < 12 {
				return nil, fmt.Errorf("short tfdt")
			}
			out = append(out, f)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("segment has no movie fragment")
	}
	return out, nil
}

func (f fragment) get(b []byte) uint64 {
	if f.wide {
		return binary.BigEndian.Uint64(b[f.timeOff:])
	}
	return uint64(binary.BigEndian.Uint32(b[f.timeOff:]))
}

func (f fragment) set(b []byte, v uint64) {
	if f.wide {
		binary.BigEndian.PutUint64(b[f.timeOff:], v)
		return
	}
	binary.BigEndian.PutUint32(b[f.timeOff:], uint32(v))
}

// chooseShift picks how far to move each track back: the earliest track's
// first decode time, converted to every track's timescale, so the file
// starts at 0 and the tracks keep their relative timing. firstMs is that
// start in milliseconds.
func chooseShift(seg []byte, timescales map[uint32]uint32) (shift []platform.TrackShift, firstMs int64, err error) {
	frags, err := fragments(seg)
	if err != nil {
		return nil, 0, err
	}
	first := map[uint32]uint64{}
	for _, f := range frags {
		if _, ok := first[f.track]; !ok {
			first[f.track] = f.get(seg)
		}
	}
	var minTrack uint32
	found := false
	for id, v := range first {
		ts, ok := timescales[id]
		if !ok {
			return nil, 0, fmt.Errorf("segment track %d is not in the init section", id)
		}
		// Compare v/ts without floating point.
		if !found || v*uint64(timescales[minTrack]) < first[minTrack]*uint64(ts) {
			minTrack, found = id, true
		}
	}
	base, baseTS := first[minTrack], uint64(timescales[minTrack])
	for id, ts := range timescales {
		shift = append(shift, platform.TrackShift{TrackID: id, Timescale: ts, Ticks: base * uint64(ts) / baseTS})
	}
	slices.SortFunc(shift, func(a, b platform.TrackShift) int { return cmp.Compare(a.TrackID, b.TrackID) })
	return shift, int64(base * 1000 / baseTS), nil
}

// rebaseSegment returns seg with every fragment moved back by shift, its
// movie fragments numbered on from *seq (which it advances), and the boxes
// that carry no media dropped.
func rebaseSegment(seg []byte, shift []platform.TrackShift, seq *uint32) ([]byte, error) {
	frags, err := fragments(seg)
	if err != nil {
		return nil, err
	}
	by := map[uint32]uint64{}
	for _, s := range shift {
		by[s.TrackID] = s.Ticks
	}
	out := append([]byte(nil), seg...)
	for _, f := range frags {
		d, ok := by[f.track]
		if !ok {
			return nil, fmt.Errorf("segment track %d is not in the init section", f.track)
		}
		v := f.get(out)
		if v < d {
			return nil, fmt.Errorf("segment of track %d starts before the first segment", f.track)
		}
		f.set(out, v-d)
	}
	top, err := boxes(out, 0, len(out))
	if err != nil {
		return nil, err
	}
	kept := make([]byte, 0, len(out))
	for _, x := range top {
		switch x.typ {
		case "styp", "sidx", "emsg", "prft":
			continue
		case "moof":
			mfhd, ok, err := child(out, x, "mfhd")
			if err != nil || !ok || mfhd.end-mfhd.body < 8 {
				return nil, fmt.Errorf("movie fragment without mfhd")
			}
			*seq++
			binary.BigEndian.PutUint32(out[mfhd.body+4:], *seq)
		}
		kept = append(kept, out[x.start:x.end]...)
	}
	return kept, nil
}
