package main

import (
	"bufio"
	"bytes"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu"
	"github.com/schollz/progressbar/v3"
	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
	"golang.org/x/term"
)

const (
	QualityOriginal    = 100 // 100 or 0: uncompressed original
	QualityRecommended = 75  // Default: high quality, ~85% size reduction
	QualityCompact     = 60  // Compact: balanced, ~90% size reduction
	QualitySmall       = 45  // Small: mobile/email, ~94% size reduction
)

// SizeEstimates contains estimated file sizes for different quality presets
type SizeEstimates struct {
	PageCount        int
	OriginalBytes    int64
	RecommendedBytes int64
	CompactBytes     int64
	SmallBytes       int64
}

// formatBytes formats byte count into human-readable string (e.g. "24.5 MB")
func formatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

// calcReduction computes percent size reduction
func calcReduction(original, target int64) float64 {
	if original <= 0 {
		return 0
	}
	red := (1.0 - float64(target)/float64(original)) * 100
	if red < 0 {
		return 0
	}
	return red
}

// convertToRGB composites an image onto a white background to avoid black transparency in JPEG
func convertToRGB(src image.Image) image.Image {
	bounds := src.Bounds()
	dst := image.NewRGBA(bounds)
	draw.Draw(dst, bounds, &image.Uniform{color.White}, image.Point{}, draw.Src)
	draw.Draw(dst, bounds, src, bounds.Min, draw.Over)
	return dst
}

// compressImageToJPEG decodes an image and encodes it to JPEG at the specified quality (1-100)
func compressImageToJPEG(data []byte, quality int) ([]byte, error) {
	if quality <= 0 || quality >= 100 {
		return data, nil
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decode error: %w", err)
	}

	rgbImg := convertToRGB(img)
	var buf bytes.Buffer
	err = jpeg.Encode(&buf, rgbImg, &jpeg.Options{Quality: quality})
	if err != nil {
		return nil, fmt.Errorf("jpeg encode error: %w", err)
	}
	return buf.Bytes(), nil
}

// samplePage downloads the first page and calculates size estimates for each preset
func (fb *flipbook) samplePage() ([]byte, *SizeEstimates, error) {
	if len(fb.pageURLs) == 0 {
		return nil, nil, fmt.Errorf("no page URLs found")
	}

	sampleURL := cleanDownloadURL(fb.pageURLs[0])
	req, err := http.NewRequest("GET", sampleURL, nil)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Referer", fb.URL.String())
	req.Header.Set("User-Agent", "Mozilla/5.0")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("sample request failed with status: %s", resp.Status)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, err
	}

	n := int64(fb.pageCount)
	if n <= 0 {
		n = 1
	}

	estimates := &SizeEstimates{
		PageCount:     fb.pageCount,
		OriginalBytes: int64(len(data)) * n,
	}

	if c75, err := compressImageToJPEG(data, QualityRecommended); err == nil {
		sz := int64(len(c75)) * n
		if sz > estimates.OriginalBytes {
			sz = estimates.OriginalBytes
		}
		estimates.RecommendedBytes = sz
	} else {
		estimates.RecommendedBytes = int64(float64(estimates.OriginalBytes) * 0.15)
	}

	if c60, err := compressImageToJPEG(data, QualityCompact); err == nil {
		sz := int64(len(c60)) * n
		if sz > estimates.OriginalBytes {
			sz = estimates.OriginalBytes
		}
		estimates.CompactBytes = sz
	} else {
		estimates.CompactBytes = int64(float64(estimates.OriginalBytes) * 0.10)
	}

	if c45, err := compressImageToJPEG(data, QualitySmall); err == nil {
		sz := int64(len(c45)) * n
		if sz > estimates.OriginalBytes {
			sz = estimates.OriginalBytes
		}
		estimates.SmallBytes = sz
	} else {
		estimates.SmallBytes = int64(float64(estimates.OriginalBytes) * 0.06)
	}

	return data, estimates, nil
}

// fallbackEstimates provides default size estimates if sample download fails
func fallbackEstimates(pageCount int) *SizeEstimates {
	n := int64(pageCount)
	if n <= 0 {
		n = 1
	}
	orig := n * 1800 * 1024 // ~1.8 MB per page
	return &SizeEstimates{
		PageCount:        pageCount,
		OriginalBytes:    orig,
		RecommendedBytes: int64(float64(orig) * 0.14),
		CompactBytes:     int64(float64(orig) * 0.09),
		SmallBytes:       int64(float64(orig) * 0.06),
	}
}

// isInteractiveTerminal checks whether standard input is an interactive terminal
func isInteractiveTerminal() bool {
	return term.IsTerminal(int(os.Stdin.Fd()))
}

// promptCompressionChoice prompts the user in the CLI to select a quality option
func promptCompressionChoice(estimates *SizeEstimates) int {
	r75 := calcReduction(estimates.OriginalBytes, estimates.RecommendedBytes)
	r60 := calcReduction(estimates.OriginalBytes, estimates.CompactBytes)
	r45 := calcReduction(estimates.OriginalBytes, estimates.SmallBytes)

	fmt.Println()
	fmt.Println("Choose PDF download size / quality:")
	fmt.Printf("  [1] Original     : ~%-8s (100%% original quality, uncompressed)\n", formatBytes(estimates.OriginalBytes))
	fmt.Printf("  [2] Recommended  : ~%-8s (JPEG 75 - %.0f%% smaller, crisp text) [Default]\n", formatBytes(estimates.RecommendedBytes), r75)
	fmt.Printf("  [3] Compact      : ~%-8s (JPEG 60 - %.0f%% smaller, balanced)\n", formatBytes(estimates.CompactBytes), r60)
	fmt.Printf("  [4] Small        : ~%-8s (JPEG 45 - %.0f%% smaller, mobile/email)\n", formatBytes(estimates.SmallBytes), r45)
	fmt.Println("  [5] Custom       : Enter custom JPEG quality (1-100)")
	fmt.Print("\nSelect option [1-5] (default: 2): ")

	reader := bufio.NewReader(os.Stdin)
	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(input)

	switch input {
	case "1":
		return QualityOriginal
	case "2", "":
		return QualityRecommended
	case "3":
		return QualityCompact
	case "4":
		return QualitySmall
	case "5":
		fmt.Print("Enter custom JPEG quality (1-100): ")
		customStr, _ := reader.ReadString('\n')
		customStr = strings.TrimSpace(customStr)
		if q, err := strconv.Atoi(customStr); err == nil && q >= 1 && q <= 100 {
			return q
		}
		fmt.Println("Invalid input, defaulting to Recommended (75)")
		return QualityRecommended
	default:
		fmt.Println("Invalid option, defaulting to Recommended (75)")
		return QualityRecommended
	}
}

// parsePreset resolves a preset name string to a quality integer
func parsePreset(preset string) (int, bool) {
	switch strings.ToLower(strings.TrimSpace(preset)) {
	case "original", "orig", "max", "full":
		return QualityOriginal, true
	case "recommended", "high", "rec":
		return QualityRecommended, true
	case "compact", "medium", "med":
		return QualityCompact, true
	case "small", "low", "mobile":
		return QualitySmall, true
	default:
		return 0, false
	}
}

// extractPageNumber parses the page number from filenames like "Title_1_Im0.png"
func extractPageNumber(filename string) int {
	parts := strings.Split(filename, "_")
	for i := len(parts) - 1; i >= 1; i-- {
		if n, err := strconv.Atoi(parts[i]); err == nil {
			return n
		}
	}
	return 0
}

// compressPDFFile compresses an existing PDF document by extracting, re-compressing images, and reconstructing the PDF
func compressPDFFile(inputPath, outputPath string, quality int) error {
	inputPath, err := filepath.Abs(inputPath)
	if err != nil {
		return fmt.Errorf("invalid input path: %w", err)
	}

	fiIn, err := os.Stat(inputPath)
	if err != nil {
		return fmt.Errorf("cannot read input file %q: %w", inputPath, err)
	}

	if outputPath == "" {
		ext := filepath.Ext(inputPath)
		base := strings.TrimSuffix(inputPath, ext)
		outputPath = base + " (Compressed)" + ext
	}
	outputPath, err = filepath.Abs(outputPath)
	if err != nil {
		return fmt.Errorf("invalid output path: %w", err)
	}

	fmt.Printf("Compressing '%s' (%.2f MB)...\n", filepath.Base(inputPath), float64(fiIn.Size())/(1024*1024))
	pageCount, err := api.PageCountFile(inputPath)
	if err != nil {
		return fmt.Errorf("failed to count pages: %w", err)
	}
	fmt.Printf("Total pages: %d\n", pageCount)

	tmpExtract, err := os.MkdirTemp("", "anyflip_extract_*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpExtract)

	tmpComp, err := os.MkdirTemp("", "anyflip_comp_*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpComp)

	fmt.Println("Extracting page images from PDF...")
	t0 := time.Now()
	err = api.ExtractImagesFile(inputPath, tmpExtract, nil, nil)
	if err != nil {
		return fmt.Errorf("image extraction failed: %w", err)
	}

	entries, err := os.ReadDir(tmpExtract)
	if err != nil {
		return err
	}

	var imageFiles []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(e.Name()))
		if ext == ".jpg" || ext == ".jpeg" || ext == ".png" || ext == ".webp" || ext == ".tif" || ext == ".tiff" {
			imageFiles = append(imageFiles, e.Name())
		}
	}

	if len(imageFiles) == 0 {
		return fmt.Errorf("no images found in PDF")
	}

	// Sort images by page number
	sort.Slice(imageFiles, func(i, j int) bool {
		pi := extractPageNumber(imageFiles[i])
		pj := extractPageNumber(imageFiles[j])
		if pi != pj {
			return pi < pj
		}
		return imageFiles[i] < imageFiles[j]
	})

	fmt.Printf("Compressing %d images (Quality=%d)...\n", len(imageFiles), quality)
	bar := progressbar.NewOptions(len(imageFiles),
		progressbar.OptionFullWidth(),
		progressbar.OptionSetPredictTime(false),
		progressbar.OptionShowCount(),
		progressbar.OptionSetDescription("Compressing"),
	)
	defer bar.Close()

	workers := donwloadThreads
	if workers < 4 {
		workers = 4
	}

	type compResult struct {
		index int
		path  string
	}
	results := make([]string, len(imageFiles))
	queue := make(chan int, len(imageFiles))
	for i := range imageFiles {
		queue <- i
	}
	close(queue)

	var wg sync.WaitGroup
	var compErr error
	var mu sync.Mutex

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range queue {
				inName := imageFiles[idx]
				inPath := filepath.Join(tmpExtract, inName)
				outPath := filepath.Join(tmpComp, fmt.Sprintf("%05d.jpg", idx))

				data, err := os.ReadFile(inPath)
				if err != nil {
					mu.Lock()
					compErr = err
					mu.Unlock()
					continue
				}

				compData, err := compressImageToJPEG(data, quality)
				if err != nil {
					mu.Lock()
					compErr = err
					mu.Unlock()
					continue
				}

				if err := os.WriteFile(outPath, compData, 0644); err != nil {
					mu.Lock()
					compErr = err
					mu.Unlock()
					continue
				}

				mu.Lock()
				results[idx] = outPath
				mu.Unlock()
				bar.Add(1)
			}
		}()
	}
	wg.Wait()
	fmt.Println()

	if compErr != nil {
		return fmt.Errorf("error during image compression: %w", compErr)
	}

	var validPaths []string
	for _, p := range results {
		if p != "" {
			validPaths = append(validPaths, p)
		}
	}

	if len(validPaths) == 0 {
		return fmt.Errorf("no compressed images generated")
	}

	// Remove output file if it exists
	os.Remove(outputPath)

	fmt.Println("Building compressed PDF...")
	impConf := pdfcpu.DefaultImportConfig()
	chunk := int(chunkSize)
	if chunk <= 0 {
		chunk = 15
	}

	for i := 0; i < len(validPaths); i += chunk {
		end := min(i+chunk, len(validPaths))
		imagePathChunk := validPaths[i:end]
		err = api.ImportImagesFile(imagePathChunk, outputPath, impConf, nil)
		if err != nil {
			return fmt.Errorf("pdf generation failed: %w", err)
		}
	}

	fiOut, err := os.Stat(outputPath)
	if err != nil {
		return err
	}

	reduction := calcReduction(fiIn.Size(), fiOut.Size())
	fmt.Printf("\nCompression Complete in %v!\n", time.Since(t0).Round(time.Millisecond))
	fmt.Printf("Original size  : %s\n", formatBytes(fiIn.Size()))
	fmt.Printf("Compressed size: %s\n", formatBytes(fiOut.Size()))
	fmt.Printf("Size reduction : %.1f%%\n", reduction)
	fmt.Printf("Output file    : %s\n", outputPath)

	return nil
}
