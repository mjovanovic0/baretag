package iso

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
)

// A trailer is a payload written after the end of the image, with a header
// sector describing it.
//
// This exists because the obvious approach, adding the payload as a real file
// in the ISO9660 tree, means telling the volume descriptors, the MBR and the
// GPT that the image got bigger. All of that is straightforward and it boots
// under OVMF, but server firmware is stricter than a virtual machine and a
// partition table it dislikes turns into "no bootable device", with no clue as
// to which field it objected to.
//
// A trailer touches none of it. Every byte of the original image is left
// exactly as it was, apart from the Ignition region, which is the one place
// these images are designed to be written to. The payload is read at boot with
// dd from the medium, at the offset recorded here.

const (
	trailerMagic   = "QRINVTRL"
	trailerVersion = 1
	// TrailerHeaderSectors is the size of the header, in ISO sectors.
	TrailerHeaderSectors = 1
)

// Trailer describes a payload appended to an image.
type Trailer struct {
	// HeaderSector is the sector the header itself occupies.
	HeaderSector uint32
	// PayloadSector is the first sector of the payload.
	PayloadSector uint32
	// PayloadSectors is how many sectors the payload occupies.
	PayloadSectors uint32
	// PayloadBytes is the exact payload length, which is usually shorter than
	// the sectors holding it.
	PayloadBytes uint64
	// PlainBytes is the length after decompression, for reporting only.
	PlainBytes uint64
	// SHA256 is the checksum of the payload as stored.
	SHA256 [32]byte
}

// AppendTrailer writes a payload after the end of the image and returns where
// it landed. Nothing already in the image is modified.
func (im *Image) AppendTrailer(payload []byte, plainLen uint64) (*Trailer, error) {
	if im.f == nil {
		return nil, fmt.Errorf("image is not open for writing")
	}

	// Start on a sector boundary past everything that is already there.
	start := uint32((im.size + SectorSize - 1) / SectorSize)
	t := &Trailer{
		HeaderSector:   start,
		PayloadSector:  start + TrailerHeaderSectors,
		PayloadSectors: uint32((len(payload) + SectorSize - 1) / SectorSize),
		PayloadBytes:   uint64(len(payload)),
		PlainBytes:     plainLen,
		SHA256:         sha256.Sum256(payload),
	}

	header := make([]byte, SectorSize)
	copy(header, trailerMagic)
	binary.LittleEndian.PutUint32(header[8:12], trailerVersion)
	binary.LittleEndian.PutUint64(header[12:20], t.PayloadBytes)
	binary.LittleEndian.PutUint64(header[20:28], t.PlainBytes)
	copy(header[28:60], t.SHA256[:])

	if _, err := im.f.WriteAt(header, int64(t.HeaderSector)*SectorSize); err != nil {
		return nil, fmt.Errorf("write trailer header: %w", err)
	}

	body := make([]byte, int(t.PayloadSectors)*SectorSize)
	copy(body, payload)
	if _, err := im.f.WriteAt(body, int64(t.PayloadSector)*SectorSize); err != nil {
		return nil, fmt.Errorf("write trailer payload: %w", err)
	}

	im.size = int64(t.PayloadSector+t.PayloadSectors) * SectorSize
	if err := im.f.Truncate(im.size); err != nil {
		return nil, fmt.Errorf("resize image: %w", err)
	}
	return t, nil
}

// ReadTrailer finds the trailer by scanning back from the end of the image for
// its header, and checks the payload against the recorded checksum.
func (im *Image) ReadTrailer() (*Trailer, []byte, error) {
	sectors := im.size / SectorSize
	// The header sits immediately before the payload, so it is found by
	// walking back from the end; in practice it is the first thing tried.
	for s := sectors - 1; s >= 0 && s > sectors-4096; s-- {
		buf := make([]byte, SectorSize)
		if _, err := im.f.ReadAt(buf, s*SectorSize); err != nil {
			continue
		}
		if !bytes.HasPrefix(buf, []byte(trailerMagic)) {
			continue
		}

		t := &Trailer{
			HeaderSector:  uint32(s),
			PayloadSector: uint32(s) + TrailerHeaderSectors,
			PayloadBytes:  binary.LittleEndian.Uint64(buf[12:20]),
			PlainBytes:    binary.LittleEndian.Uint64(buf[20:28]),
		}
		copy(t.SHA256[:], buf[28:60])
		t.PayloadSectors = uint32((t.PayloadBytes + SectorSize - 1) / SectorSize)

		if v := binary.LittleEndian.Uint32(buf[8:12]); v != trailerVersion {
			return nil, nil, fmt.Errorf("trailer version %d is not supported", v)
		}
		payload := make([]byte, t.PayloadBytes)
		if _, err := im.f.ReadAt(payload, int64(t.PayloadSector)*SectorSize); err != nil {
			return nil, nil, fmt.Errorf("read trailer payload: %w", err)
		}
		if sha256.Sum256(payload) != t.SHA256 {
			return nil, nil, fmt.Errorf("the trailer payload does not match its checksum")
		}
		return t, payload, nil
	}
	return nil, nil, fmt.Errorf("no trailer found in this image")
}
