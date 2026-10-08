package tui

import (
	"encoding/binary"
	"hash/crc32"
	"os"
	"path/filepath"
	"testing"
)

func readFileBytes(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return data
}

func patchPNGIHDR(t *testing.T, data []byte, width, height uint32) []byte {
	t.Helper()
	patched := append([]byte(nil), data...)
	if len(patched) < 33 {
		t.Fatalf("png too short to patch: %d", len(patched))
	}
	binary.BigEndian.PutUint32(patched[16:20], width)
	binary.BigEndian.PutUint32(patched[20:24], height)
	crc := crc32.ChecksumIEEE(patched[12:29])
	binary.BigEndian.PutUint32(patched[29:33], crc)
	return patched
}

func findPNGChunk(data []byte, want string) int {
	offset := 8
	for offset+8 <= len(data) {
		length := int(binary.BigEndian.Uint32(data[offset : offset+4]))
		chunkType := string(data[offset+4 : offset+8])
		end := offset + 8 + length + 4
		if end > len(data) {
			return -1
		}
		if chunkType == want {
			return offset
		}
		offset = end
	}
	return -1
}

func patchJPEGDimensions(t *testing.T, data []byte, width, height uint16) []byte {
	t.Helper()
	patched := append([]byte(nil), data...)
	for index := 2; index+9 <= len(patched); index++ {
		if patched[index] != 0xff {
			continue
		}
		marker := patched[index+1]
		if marker == 0xc0 || marker == 0xc1 || marker == 0xc2 {
			binary.BigEndian.PutUint16(patched[index+5:index+7], height)
			binary.BigEndian.PutUint16(patched[index+7:index+9], width)
			return patched
		}
		segmentLength := int(binary.BigEndian.Uint16(patched[index+2 : index+4]))
		if segmentLength < 2 {
			break
		}
		index += segmentLength
	}
	t.Fatalf("no SOF marker found in jpeg")
	return nil
}

func TestImageLoaderRejectsOversizedPNGHeaderWithoutDecoding(t *testing.T) {
	root := t.TempDir()
	small := filepath.Join(root, "small.png")
	writePNG(t, small, 2, 2)
	huge := patchPNGIHDR(t, readFileBytes(t, small), 100000, 100000)
	hugePath := filepath.Join(root, "huge.png")
	if err := os.WriteFile(hugePath, huge, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := NewImageLoader(root, true).Load("huge.png"); err != ErrImageDimensions {
		t.Fatalf("oversized png header = %v, want %v", err, ErrImageDimensions)
	}
}

func TestImageLoaderRejectsTruncatedAndFabricatedPNG(t *testing.T) {
	root := t.TempDir()
	valid := filepath.Join(root, "valid.png")
	writePNG(t, valid, 4, 4)
	data := readFileBytes(t, valid)
	truncated := append([]byte(nil), data[:len(data)-6]...)
	if err := os.WriteFile(filepath.Join(root, "truncated.png"), truncated, 0o644); err != nil {
		t.Fatal(err)
	}
	fabricated := append([]byte(nil), data...)
	idat := findPNGChunk(fabricated, "IDAT")
	if idat < 0 {
		t.Fatal("no IDAT chunk found")
	}
	binary.BigEndian.PutUint32(fabricated[idat:idat+4], 1<<28)
	if err := os.WriteFile(filepath.Join(root, "fabricated.png"), fabricated, 0o644); err != nil {
		t.Fatal(err)
	}
	loader := NewImageLoader(root, true)
	for _, name := range []string{"truncated.png", "fabricated.png"} {
		if _, err := loader.Load(name); err != ErrImageUnsupported {
			t.Fatalf("%s = %v, want %v", name, err, ErrImageUnsupported)
		}
	}
}

func TestImageLoaderRejectsOversizedJPEGHeaderBeforeDecode(t *testing.T) {
	root := t.TempDir()
	small := filepath.Join(root, "small.jpg")
	writeJPEG(t, small, 4, 4)
	huge := patchJPEGDimensions(t, readFileBytes(t, small), 60000, 60000)
	if err := os.WriteFile(filepath.Join(root, "huge.jpg"), huge, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := NewImageLoader(root, true).Load("huge.jpg"); err != ErrImageDimensions {
		t.Fatalf("oversized jpeg header = %v, want %v", err, ErrImageDimensions)
	}
}

func TestImageLoaderRejectsOversizedGIFHeaderBeforeDecode(t *testing.T) {
	root := t.TempDir()
	small := filepath.Join(root, "small.gif")
	writeGIF(t, small, 4, 4)
	huge := append([]byte(nil), readFileBytes(t, small)...)
	binary.LittleEndian.PutUint16(huge[6:8], 60000)
	binary.LittleEndian.PutUint16(huge[8:10], 60000)
	if err := os.WriteFile(filepath.Join(root, "huge.gif"), huge, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := NewImageLoader(root, true).Load("huge.gif"); err != ErrImageDimensions {
		t.Fatalf("oversized gif header = %v, want %v", err, ErrImageDimensions)
	}
}

func TestValidatePNGStructureCases(t *testing.T) {
	root := t.TempDir()
	valid := filepath.Join(root, "valid.png")
	writePNG(t, valid, 3, 3)
	data := readFileBytes(t, valid)
	iend := findPNGChunk(data, "IEND")
	if iend < 0 {
		t.Fatal("no IEND chunk found")
	}
	withoutIEND := append([]byte(nil), data[:iend]...)
	truncatedCRC := append([]byte(nil), data[:iend+10]...)
	cases := map[string]struct {
		payload []byte
		valid   bool
	}{
		"valid":          {data, true},
		"short":          {data[:20], false},
		"unknown-format": {[]byte("not a png at all"), false},
		"missing-iend":   {withoutIEND, false},
		"truncated-crc":  {truncatedCRC, false},
	}
	for name, testCase := range cases {
		err := validatePNGStructure(testCase.payload)
		if testCase.valid && err != nil {
			t.Fatalf("%s rejected: %v", name, err)
		}
		if !testCase.valid && err == nil {
			t.Fatalf("%s was accepted", name)
		}
	}
}

func TestImageLoaderRejectsTruncatedAndFabricatedWebP(t *testing.T) {
	root := t.TempDir()
	valid := craftWebP("VP8L", 20, 10)
	if err := os.WriteFile(filepath.Join(root, "valid.webp"), valid, 0o644); err != nil {
		t.Fatal(err)
	}
	truncated := append([]byte(nil), valid[:len(valid)-1]...)
	if err := os.WriteFile(filepath.Join(root, "truncated.webp"), truncated, 0o644); err != nil {
		t.Fatal(err)
	}
	fabricated := append([]byte(nil), valid...)
	binary.LittleEndian.PutUint32(fabricated[16:20], 1<<28)
	if err := os.WriteFile(filepath.Join(root, "fabricated.webp"), fabricated, 0o644); err != nil {
		t.Fatal(err)
	}
	loader := NewImageLoader(root, true)
	if _, err := loader.Load("valid.webp"); err != nil {
		t.Fatalf("valid webp rejected: %v", err)
	}
	for _, name := range []string{"truncated.webp", "fabricated.webp"} {
		if _, err := loader.Load(name); err != ErrImageUnsupported {
			t.Fatalf("%s = %v, want %v", name, err, ErrImageUnsupported)
		}
	}
}

func craftWebPChunks(chunks ...[]byte) []byte {
	body := []byte("WEBP")
	for _, chunk := range chunks {
		body = append(body, chunk...)
	}
	data := make([]byte, 8+len(body))
	copy(data[0:4], "RIFF")
	binary.LittleEndian.PutUint32(data[4:8], uint32(len(body)))
	copy(data[8:], body)
	return data
}

func TestWebPDimensionsSkipsUnknownChunks(t *testing.T) {
	single := craftWebP("VP8L", 100, 50)
	data := craftWebPChunks(craftWebPChunk("ICCP", []byte{1, 2, 3}), single[12:])
	width, height, err := webPDimensions(data)
	if err != nil {
		t.Fatalf("multi-chunk webp: %v", err)
	}
	if width != 100 || height != 50 {
		t.Fatalf("dimensions = %dx%d, want 100x50", width, height)
	}
	unknownOnly := craftWebPChunks(craftWebPChunk("ICCP", []byte{9}))
	if _, _, err := webPDimensions(unknownOnly); err != ErrImageUnsupported {
		t.Fatalf("unknown-only webp = %v, want %v", err, ErrImageUnsupported)
	}
}

func TestWebPChunkDimensionsRejectsMalformedChunks(t *testing.T) {
	cases := []struct {
		fourCC  string
		payload []byte
	}{
		{"VP8 ", []byte{0, 0, 0}},
		{"VP8L", []byte{0x2f}},
		{"VP8X", []byte{1, 2, 3}},
	}
	for _, testCase := range cases {
		data := craftWebPChunks(craftWebPChunk(testCase.fourCC, testCase.payload))
		if _, _, err := webPDimensions(data); err != ErrImageUnsupported {
			t.Fatalf("%s = %v, want %v", testCase.fourCC, err, ErrImageUnsupported)
		}
	}
	if _, _, ok := webPChunkDimensions("JUNK", []byte{1, 2, 3, 4}); ok {
		t.Fatal("unknown chunk fourCC reported dimensions")
	}
}

func TestImageLoaderRejectsOversizedWebPHeader(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "huge.webp"), craftWebP("VP8X", 5000, 1), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "many.webp"), craftWebP("VP8X", 3000, 2000), 0o644); err != nil {
		t.Fatal(err)
	}
	loader := NewImageLoader(root, true)
	if _, err := loader.Load("huge.webp"); err != ErrImageDimensions {
		t.Fatalf("huge webp = %v, want %v", err, ErrImageDimensions)
	}
	if _, err := loader.Load("many.webp"); err != ErrImageTooManyPixels {
		t.Fatalf("many-pixel webp = %v, want %v", err, ErrImageTooManyPixels)
	}
}

func TestImageLoaderCheckBoundsIsOverflowSafe(t *testing.T) {
	loader := NewImageLoader(t.TempDir(), true)
	loader.maxPixels = 8
	if err := loader.checkBounds(2, 4); err != nil {
		t.Fatalf("2x4 within 8 pixels: %v", err)
	}
	if err := loader.checkBounds(3, 3); err != ErrImageTooManyPixels {
		t.Fatalf("3x3 over 8 pixels = %v, want %v", err, ErrImageTooManyPixels)
	}
	if err := loader.checkBounds(0, 4); err != ErrImageDimensions {
		t.Fatalf("zero width = %v, want %v", err, ErrImageDimensions)
	}
	loader.maxPixels = 0
	if err := loader.checkBounds(1, 1); err != ErrImageTooManyPixels {
		t.Fatalf("disabled pixel limit = %v, want %v", err, ErrImageTooManyPixels)
	}
}
