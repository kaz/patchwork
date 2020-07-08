package patchwork

import (
	"encoding/binary"
)

type (
	directoryRecord struct {
		raw []byte

		ExtentLocation uint32
		ExtentSize     uint32
		Identifier     string

		RockRidgeName string
		SystemUse     [][]byte
	}
)

func marshalUint32(i uint32) []byte {
	le := make([]byte, 4)
	binary.LittleEndian.PutUint32(le, i)
	be := make([]byte, 4)
	binary.BigEndian.PutUint32(be, i)
	return append(le, be...)
}

func unmarshalDirectoryRecord(raw []byte) *directoryRecord {
	r := &directoryRecord{raw: raw}

	// Extent location is encoded as little-endian in [2:6]
	r.ExtentLocation = binary.LittleEndian.Uint32(raw[2:6])

	// Extent size is encoded as little-endian in [10:14]
	r.ExtentSize = binary.LittleEndian.Uint32(raw[10:14])

	// Length of identifier is located in [32], followed by identifier string.
	idLen := raw[32]
	r.Identifier = string(raw[33 : 33+idLen])

	// System-use fields follows after identifier, with 1 byte padding when length of identifier is even
	systemUseOffset := int(33 + idLen)
	if idLen%2 == 0 {
		systemUseOffset++
	}

	r.SystemUse = [][]byte{}
	for i := systemUseOffset; i+2 < len(raw); i += int(raw[i+2]) {
		// Each system-use field has its size at 3rd byte of field self.
		fieldLen := int(raw[i+2])
		field := raw[i : i+fieldLen]
		r.SystemUse = append(r.SystemUse, field)

		// A field which has `NM` signature is file name defined in Rock Ridge Interchange Protocol.
		if string(field[:2]) == "NM" {
			// Name starts at 6th byte of field.
			r.RockRidgeName = string(field[5:])
		}
	}

	return r
}
func (r *directoryRecord) marshal() []byte {
	raw := r.raw[:2]

	raw = append(raw, marshalUint32(r.ExtentLocation)...)
	raw = append(raw, marshalUint32(r.ExtentSize)...)

	raw = append(raw, r.raw[18:32]...)

	raw = append(raw, byte(len(r.Identifier)))
	raw = append(raw, r.Identifier...)

	if len(r.Identifier)%2 == 0 {
		raw = append(raw, 0)
	}

	for _, field := range r.SystemUse {
		if string(field[:2]) == "NM" {
			field = field[:5]
			field = append(field, r.RockRidgeName...)
			field[2] = byte(len(field))
		}
		raw = append(raw, field...)
	}

	if len(raw)%2 == 1 {
		raw = append(raw, 0)
	}

	raw[0] = byte(len(raw))
	return raw
}

func (r *directoryRecord) clone() *directoryRecord {
	return unmarshalDirectoryRecord(r.marshal())
}
