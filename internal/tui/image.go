package tui

import (
	"bytes"
	"errors"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const (
	imageMaxBytes     = 4 << 20
	imageMaxDimension = 4096
	imageMaxPixels    = 4_000_000
	imageCacheMax     = 32 << 20
	imageMaxPathLen   = 4096
)

var (
	ErrImageDisabled      = errors.New("tui: inline images are disabled")
	ErrImageTooLarge      = errors.New("tui: image exceeds the size limit")
	ErrImageTooManyPixels = errors.New("tui: image exceeds the pixel limit")
	ErrImageDimensions    = errors.New("tui: image dimensions are out of range")
	ErrImageUnsupported   = errors.New("tui: unsupported image format")
	ErrImageUnsafePath    = errors.New("tui: image path is outside the workspace")
	ErrImageNotRegular    = errors.New("tui: image is not a regular file")
)

type ImageFormat int

const (
	ImageFormatUnknown ImageFormat = iota
	ImageFormatPNG
	ImageFormatJPEG
	ImageFormatGIF
	ImageFormatWebP
)

func (f ImageFormat) String() string {
	switch f {
	case ImageFormatPNG:
		return "png"
	case ImageFormatJPEG:
		return "jpeg"
	case ImageFormatGIF:
		return "gif"
	case ImageFormatWebP:
		return "webp"
	}
	return "unknown"
}

type LoadedImage struct {
	Format          ImageFormat
	Width           int
	Height          int
	Data            []byte
	CanonicalPNG    bool
	WebPPassthrough bool
	SizeBytes       int
}

type ImageLoader struct {
	mu            sync.Mutex
	workspaceRoot string
	enabled       bool
	maxBytes      int
	maxDimension  int
	maxPixels     int
	cacheMax      int
	cache         map[string]*LoadedImage
	order         []string
	cacheBytes    int
	onEvict       func(string)
	openFile      func(string) (*os.File, error)
	openHook      imageOpenHook
}

func NewImageLoader(workspaceRoot string, enabled bool) *ImageLoader {
	return &ImageLoader{
		workspaceRoot: workspaceRoot,
		enabled:       enabled,
		maxBytes:      imageMaxBytes,
		maxDimension:  imageMaxDimension,
		maxPixels:     imageMaxPixels,
		cacheMax:      imageCacheMax,
		cache:         map[string]*LoadedImage{},
	}
}

func (l *ImageLoader) Enabled() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.enabled
}

func (l *ImageLoader) SetEnabled(enabled bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.enabled == enabled {
		return
	}
	l.enabled = enabled
	if !enabled {
		l.clearLocked()
	}
}

func (l *ImageLoader) SetOpenFile(open func(string) (*os.File, error)) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.openFile = open
}

func (l *ImageLoader) CacheBytes() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.cacheBytes
}

func (l *ImageLoader) Clear() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.clearLocked()
}

func (l *ImageLoader) clearLocked() {
	l.cache = map[string]*LoadedImage{}
	l.order = nil
	l.cacheBytes = 0
}

func (l *ImageLoader) SetOnEvict(fn func(string)) {
	l.mu.Lock()
	l.onEvict = fn
	l.mu.Unlock()
}

func (l *ImageLoader) notifyEvictLocked(path string) {
	if l.onEvict != nil {
		l.onEvict(path)
	}
}

func (l *ImageLoader) Evict(path string) {
	if resolved, err := l.ResolvePath(path); err == nil {
		path = resolved
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.cache[path]; !ok {
		return
	}
	delete(l.cache, path)
	for index, candidate := range l.order {
		if candidate == path {
			l.order = append(l.order[:index], l.order[index+1:]...)
			break
		}
	}
	l.cacheBytes = 0
	for _, cached := range l.cache {
		l.cacheBytes += len(cached.Data)
	}
	l.notifyEvictLocked(path)
}

func (l *ImageLoader) ResolvePath(path string) (string, error) {
	_, absolute, err := l.resolveWorkspacePath(path)
	return absolute, err
}

func (l *ImageLoader) resolveWorkspacePath(path string) (string, string, error) {
	if len(path) > imageMaxPathLen {
		return "", "", ErrImageUnsafePath
	}
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return "", "", ErrImageUnsafePath
	}
	lower := strings.ToLower(trimmed)
	for _, scheme := range []string{"http:", "https:", "data:", "ftp:", "file:", "//"} {
		if strings.HasPrefix(lower, scheme) {
			return "", "", ErrImageUnsafePath
		}
	}
	if strings.Contains(trimmed, "\x00") {
		return "", "", ErrImageUnsafePath
	}
	if filepath.IsAbs(trimmed) || filepath.VolumeName(trimmed) != "" {
		return "", "", ErrImageUnsafePath
	}
	if !filepath.IsLocal(trimmed) {
		return "", "", ErrImageUnsafePath
	}
	for _, component := range strings.Split(trimmed, string(filepath.Separator)) {
		if component == ".." {
			return "", "", ErrImageUnsafePath
		}
	}
	cleaned := filepath.Clean(trimmed)
	if cleaned == "." || cleaned == "" {
		return "", "", ErrImageUnsafePath
	}
	root := l.workspaceRoot
	if root == "" {
		return "", "", ErrImageUnsafePath
	}
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return "", "", ErrImageUnsafePath
	}
	absolutePath, err := filepath.Abs(filepath.Join(absoluteRoot, cleaned))
	if err != nil {
		return "", "", ErrImageUnsafePath
	}
	relative, err := filepath.Rel(absoluteRoot, absolutePath)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", "", ErrImageUnsafePath
	}
	return cleaned, absolutePath, nil
}

func (l *ImageLoader) Load(path string) (*LoadedImage, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.enabled {
		return nil, ErrImageDisabled
	}
	relative, absolute, err := l.resolveWorkspacePath(path)
	if err != nil {
		return nil, err
	}
	if cached, ok := l.cache[absolute]; ok {
		return cached, nil
	}
	image, err := l.loadFile(relative)
	if err != nil {
		return nil, err
	}
	l.storeLocked(absolute, image)
	return image, nil
}

func (l *ImageLoader) loadFile(relative string) (*LoadedImage, error) {
	file, err := anchoredImageOpen(l.workspaceRoot, relative, l.openFile, l.openHook)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return nil, ErrImageNotRegular
	}
	if opened.Size() > int64(l.maxBytes) {
		return nil, ErrImageTooLarge
	}
	data, err := io.ReadAll(io.LimitReader(file, int64(l.maxBytes)+1))
	if err != nil {
		return nil, err
	}
	if len(data) > l.maxBytes {
		return nil, ErrImageTooLarge
	}
	return l.decode(data)
}

func (l *ImageLoader) decode(data []byte) (*LoadedImage, error) {
	format := detectImageFormat(data)
	switch format {
	case ImageFormatPNG:
		config, err := png.DecodeConfig(bytes.NewReader(data))
		if err != nil {
			return nil, ErrImageUnsupported
		}
		if err := l.checkBounds(config.Width, config.Height); err != nil {
			return nil, err
		}
		if err := validatePNGStructure(data); err != nil {
			return nil, err
		}
		return &LoadedImage{Format: format, Width: config.Width, Height: config.Height, Data: data, SizeBytes: len(data)}, nil
	case ImageFormatJPEG:
		config, err := jpeg.DecodeConfig(bytes.NewReader(data))
		if err != nil {
			return nil, ErrImageUnsupported
		}
		if err := l.checkBounds(config.Width, config.Height); err != nil {
			return nil, err
		}
		decoded, err := jpeg.Decode(bytes.NewReader(data))
		if err != nil {
			return nil, ErrImageUnsupported
		}
		return l.canonicalize(format, decoded)
	case ImageFormatGIF:
		config, err := gif.DecodeConfig(bytes.NewReader(data))
		if err != nil {
			return nil, ErrImageUnsupported
		}
		if err := l.checkBounds(config.Width, config.Height); err != nil {
			return nil, err
		}
		decoded, err := gif.Decode(bytes.NewReader(data))
		if err != nil {
			return nil, ErrImageUnsupported
		}
		return l.canonicalize(format, decoded)
	case ImageFormatWebP:
		width, height, err := webPDimensions(data)
		if err != nil {
			return nil, err
		}
		if err := l.checkBounds(width, height); err != nil {
			return nil, err
		}
		return &LoadedImage{Format: format, Width: width, Height: height, Data: data, WebPPassthrough: true, SizeBytes: len(data)}, nil
	}
	return nil, ErrImageUnsupported
}

func (l *ImageLoader) canonicalize(format ImageFormat, decoded image.Image) (*LoadedImage, error) {
	bounds := decoded.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()
	if err := l.checkBounds(width, height); err != nil {
		return nil, err
	}
	var buffer bytes.Buffer
	encoder := &png.Encoder{CompressionLevel: png.DefaultCompression}
	if err := encoder.Encode(&buffer, decoded); err != nil {
		return nil, ErrImageUnsupported
	}
	return &LoadedImage{
		Format:       format,
		Width:        width,
		Height:       height,
		Data:         buffer.Bytes(),
		CanonicalPNG: true,
		SizeBytes:    buffer.Len(),
	}, nil
}

func (l *ImageLoader) checkBounds(width, height int) error {
	if width <= 0 || height <= 0 || width > l.maxDimension || height > l.maxDimension {
		return ErrImageDimensions
	}
	if l.maxPixels <= 0 || height > l.maxPixels/width {
		return ErrImageTooManyPixels
	}
	return nil
}

func (l *ImageLoader) storeLocked(path string, image *LoadedImage) {
	if len(image.Data) > l.cacheMax {
		return
	}
	for l.cacheBytes+len(image.Data) > l.cacheMax && len(l.order) > 0 {
		oldest := l.order[0]
		l.order = l.order[1:]
		if cached, ok := l.cache[oldest]; ok {
			l.cacheBytes -= len(cached.Data)
			delete(l.cache, oldest)
			l.notifyEvictLocked(oldest)
		}
	}
	l.cache[path] = image
	l.order = append(l.order, path)
	l.cacheBytes += len(image.Data)
}

func detectImageFormat(data []byte) ImageFormat {
	if len(data) >= 8 && bytes.Equal(data[:8], []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}) {
		return ImageFormatPNG
	}
	if len(data) >= 3 && data[0] == 0xff && data[1] == 0xd8 && data[2] == 0xff {
		return ImageFormatJPEG
	}
	if len(data) >= 6 && (bytes.Equal(data[:6], []byte("GIF87a")) || bytes.Equal(data[:6], []byte("GIF89a"))) {
		return ImageFormatGIF
	}
	if len(data) >= 12 && bytes.Equal(data[:4], []byte("RIFF")) && bytes.Equal(data[8:12], []byte("WEBP")) {
		return ImageFormatWebP
	}
	return ImageFormatUnknown
}

type placementKey struct {
	cacheKey   string
	generation uint64
	x, y       int
	rows, cols int
}

type placementRecord struct {
	imageID   int
	placement int
	cacheKey  string
}

type PlacementRequest struct {
	CacheKey   string
	Source     string
	X          int
	Y          int
	Rows       int
	Columns    int
	Generation uint64
	Image      *LoadedImage
	Fullscreen bool
}

type PlacementRender struct {
	X        int
	Y        int
	Sequence string
}

type ReconcileResult struct {
	Deletions []string
	Renders   []PlacementRender
}

type ImagePlacements struct {
	mu            sync.Mutex
	protocol      GraphicsProtocol
	nextID        int
	nextPlacement int
	active        map[int]bool
	reconciled    map[placementKey]placementRecord
}

func NewImagePlacements(protocol GraphicsProtocol) *ImagePlacements {
	return &ImagePlacements{
		protocol:   protocol,
		nextID:     1,
		active:     map[int]bool{},
		reconciled: map[placementKey]placementRecord{},
	}
}

func (p *ImagePlacements) Protocol() GraphicsProtocol {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.protocol
}

func (p *ImagePlacements) Allocate() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	id := p.nextID
	p.nextID++
	p.active[id] = true
	return id
}

func (p *ImagePlacements) Active() []int {
	p.mu.Lock()
	defer p.mu.Unlock()
	ids := make([]int, 0, len(p.active))
	for id := range p.active {
		ids = append(ids, id)
	}
	return ids
}

func (p *ImagePlacements) Release(id int) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.active[id] {
		return ""
	}
	delete(p.active, id)
	if p.protocol == GraphicsKitty {
		return KittyDeleteImage(id)
	}
	return ""
}

func (p *ImagePlacements) ReleaseAll() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	empty := len(p.active) == 0 && len(p.reconciled) == 0
	p.active = map[int]bool{}
	p.reconciled = map[placementKey]placementRecord{}
	if empty || p.protocol != GraphicsKitty {
		return ""
	}
	return KittyFreeAll()
}

func (p *ImagePlacements) SetProtocol(protocol GraphicsProtocol) {
	p.mu.Lock()
	p.protocol = protocol
	p.mu.Unlock()
}

func (p *ImagePlacements) reconcileKey(request PlacementRequest) placementKey {
	return placementKey{
		cacheKey:   request.CacheKey,
		generation: request.Generation,
		x:          request.X,
		y:          request.Y,
		rows:       request.Rows,
		cols:       request.Columns,
	}
}

func (p *ImagePlacements) Reconcile(requests []PlacementRequest) ReconcileResult {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.protocol == GraphicsNone {
		p.reconciled = map[placementKey]placementRecord{}
		return ReconcileResult{}
	}
	desired := make(map[placementKey]PlacementRequest, len(requests))
	order := make([]placementKey, 0, len(requests))
	for _, request := range requests {
		key := p.reconcileKey(request)
		if _, exists := desired[key]; exists {
			continue
		}
		desired[key] = request
		order = append(order, key)
	}
	var result ReconcileResult
	for key, record := range p.reconciled {
		if _, keep := desired[key]; keep {
			continue
		}
		delete(p.reconciled, key)
		if p.protocol == GraphicsKitty {
			result.Deletions = append(result.Deletions, KittyDeletePlacement(record.imageID, record.placement))
		}
	}
	for _, key := range order {
		if _, exists := p.reconciled[key]; exists {
			continue
		}
		request := desired[key]
		imageID := p.nextID
		p.nextID++
		placement := p.nextPlacement + 1
		p.nextPlacement++
		p.reconciled[key] = placementRecord{imageID: imageID, placement: placement, cacheKey: request.CacheKey}
		sequence, ok := ImageRenderSequenceFor(p.protocol, request.Image, request.Fullscreen, imageID, placement, request.Columns, request.Rows)
		if !ok {
			continue
		}
		result.Renders = append(result.Renders, PlacementRender{X: request.X, Y: request.Y, Sequence: sequence})
	}
	return result
}

func (p *ImagePlacements) FreeCacheKey(cacheKey string) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.protocol != GraphicsKitty {
		return ""
	}
	var builder strings.Builder
	for key, record := range p.reconciled {
		if record.cacheKey != cacheKey {
			continue
		}
		builder.WriteString(KittyFreeImage(record.imageID))
		delete(p.reconciled, key)
	}
	return builder.String()
}

func ImageRenderSequence(protocol GraphicsProtocol, image *LoadedImage, imageID, placement, columns, rows int) (string, bool) {
	return ImageRenderSequenceFor(protocol, image, false, imageID, placement, columns, rows)
}

func ImageRenderSequenceFor(protocol GraphicsProtocol, image *LoadedImage, fullscreen bool, imageID, placement, columns, rows int) (string, bool) {
	if image == nil {
		return "", false
	}
	switch protocol {
	case GraphicsKitty:
		if image.WebPPassthrough {
			return "", false
		}
		return KittyTransmit(imageID, placement, columns, rows, image.Data), true
	case GraphicsITerm2:
		if fullscreen {
			return "", false
		}
		if image.WebPPassthrough {
			return ITerm2Inline("image.webp", image.Data, columns, rows), true
		}
		return ITerm2Inline("image.png", image.Data, columns, rows), true
	}
	return "", false
}
