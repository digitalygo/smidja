package tui

import "encoding/binary"

const (
	pngSignatureLength = 8
	pngChunkHeaderLen  = 8
	pngChunkCRCLength  = 4
	pngIHDRLength      = 13
	webPHeaderLength   = 12
)

func validatePNGStructure(data []byte) error {
	if len(data) < pngSignatureLength+pngChunkHeaderLen+pngIHDRLength+pngChunkCRCLength {
		return ErrImageUnsupported
	}
	offset := pngSignatureLength
	firstChunk := true
	sawImageData := false
	for offset < len(data) {
		if offset+pngChunkHeaderLen > len(data) {
			return ErrImageUnsupported
		}
		length := int64(binary.BigEndian.Uint32(data[offset : offset+4]))
		chunkType := string(data[offset+4 : offset+8])
		end := int64(offset) + pngChunkHeaderLen + length + pngChunkCRCLength
		if end > int64(len(data)) {
			return ErrImageUnsupported
		}
		if firstChunk {
			if chunkType != "IHDR" || length != pngIHDRLength {
				return ErrImageUnsupported
			}
			firstChunk = false
		}
		switch chunkType {
		case "IDAT":
			sawImageData = true
		case "IEND":
			if length != 0 || !sawImageData || end != int64(len(data)) {
				return ErrImageUnsupported
			}
			return nil
		}
		offset = int(end)
	}
	return ErrImageUnsupported
}

func webPDimensions(data []byte) (int, int, error) {
	if len(data) < webPHeaderLength || string(data[0:4]) != "RIFF" || string(data[8:12]) != "WEBP" {
		return 0, 0, ErrImageUnsupported
	}
	riffSize := int64(binary.LittleEndian.Uint32(data[4:8]))
	if riffSize < 4 || riffSize > int64(len(data))-8 {
		return 0, 0, ErrImageUnsupported
	}
	end := int(riffSize) + 8
	for offset := webPHeaderLength; offset+8 <= end; {
		if offset+8 > len(data) {
			return 0, 0, ErrImageUnsupported
		}
		fourCC := string(data[offset : offset+4])
		size := int64(binary.LittleEndian.Uint32(data[offset+4 : offset+8]))
		payload := offset + 8
		if size < 0 || int64(payload)+size > int64(end) {
			return 0, 0, ErrImageUnsupported
		}
		width, height, ok := webPChunkDimensions(fourCC, data[payload:payload+int(size)])
		if ok {
			return width, height, nil
		}
		if fourCC == "VP8 " || fourCC == "VP8L" || fourCC == "VP8X" {
			return 0, 0, ErrImageUnsupported
		}
		next := payload + int(size)
		if size%2 == 1 && next < end {
			next++
		}
		if next > end || next <= offset {
			return 0, 0, ErrImageUnsupported
		}
		offset = next
	}
	return 0, 0, ErrImageUnsupported
}

func webPChunkDimensions(fourCC string, payload []byte) (int, int, bool) {
	switch fourCC {
	case "VP8 ":
		if len(payload) < 10 || payload[3] != 0x9d || payload[4] != 0x01 || payload[5] != 0x2a {
			return 0, 0, false
		}
		width := int(binary.LittleEndian.Uint16(payload[6:8])) & 0x3fff
		height := int(binary.LittleEndian.Uint16(payload[8:10])) & 0x3fff
		return width, height, true
	case "VP8L":
		if len(payload) < 5 || payload[0] != 0x2f {
			return 0, 0, false
		}
		bits := binary.LittleEndian.Uint32(payload[1:5])
		width := int(bits&0x3fff) + 1
		height := int((bits>>14)&0x3fff) + 1
		return width, height, true
	case "VP8X":
		if len(payload) < 10 {
			return 0, 0, false
		}
		width := int(payload[4]) | int(payload[5])<<8 | int(payload[6])<<16
		height := int(payload[7]) | int(payload[8])<<8 | int(payload[9])<<16
		return width + 1, height + 1, true
	}
	return 0, 0, false
}
