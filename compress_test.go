package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		bytes int64
		want  string
	}{
		{500, "500 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1048576, "1.0 MB"},
		{104857600, "100.0 MB"},
	}

	for _, tc := range tests {
		got := formatBytes(tc.bytes)
		if got != tc.want {
			t.Errorf("formatBytes(%d) = %q, want %q", tc.bytes, got, tc.want)
		}
	}
}

func TestCalcReduction(t *testing.T) {
	tests := []struct {
		orig, target int64
		want         float64
	}{
		{100, 20, 80.0},
		{100, 100, 0.0},
		{100, 120, 0.0},
		{0, 50, 0.0},
		{200, 50, 75.0},
	}

	for _, tc := range tests {
		got := calcReduction(tc.orig, tc.target)
		if got != tc.want {
			t.Errorf("calcReduction(%d, %d) = %f, want %f", tc.orig, tc.target, got, tc.want)
		}
	}
}

func TestParsePreset(t *testing.T) {
	tests := []struct {
		preset string
		wantQ  int
		wantOk bool
	}{
		{"recommended", QualityRecommended, true},
		{"RECOMMENDED", QualityRecommended, true},
		{"high", QualityRecommended, true},
		{"compact", QualityCompact, true},
		{"medium", QualityCompact, true},
		{"small", QualitySmall, true},
		{"low", QualitySmall, true},
		{"original", QualityOriginal, true},
		{"unknown", 0, false},
	}

	for _, tc := range tests {
		gotQ, gotOk := parsePreset(tc.preset)
		if gotOk != tc.wantOk || gotQ != tc.wantQ {
			t.Errorf("parsePreset(%q) = (%d, %v), want (%d, %v)", tc.preset, gotQ, gotOk, tc.wantQ, tc.wantOk)
		}
	}
}

func TestExtractPageNumber(t *testing.T) {
	tests := []struct {
		filename string
		want     int
	}{
		{"Book_1_Im0.png", 1},
		{"Book_42_Im0.png", 42},
		{"Book_114_Im0.png", 114},
		{"simple.png", 0},
	}

	for _, tc := range tests {
		got := extractPageNumber(tc.filename)
		if got != tc.want {
			t.Errorf("extractPageNumber(%q) = %d, want %d", tc.filename, got, tc.want)
		}
	}
}

func TestCompressImageToJPEG(t *testing.T) {
	// Create a 60x60 test image with RGBA semi-transparency
	img := image.NewRGBA(image.Rect(0, 0, 60, 60))
	for x := 0; x < 60; x++ {
		for y := 0; y < 60; y++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 4), G: uint8(y * 4), B: 128, A: 200})
		}
	}

	var buf bytes.Buffer
	err := png.Encode(&buf, img)
	if err != nil {
		t.Fatalf("failed to encode test PNG: %v", err)
	}
	pngData := buf.Bytes()

	// Quality 100 should return original data unchanged
	q100, err := compressImageToJPEG(pngData, 100)
	if err != nil {
		t.Fatalf("compressImageToJPEG(100) failed: %v", err)
	}
	if len(q100) != len(pngData) {
		t.Errorf("expected quality 100 to return original data")
	}

	// Quality 75 should return valid JPEG data
	q75, err := compressImageToJPEG(pngData, 75)
	if err != nil {
		t.Fatalf("compressImageToJPEG(75) failed: %v", err)
	}
	if len(q75) == 0 {
		t.Fatalf("expected non-empty output")
	}
	// JPEG starts with 0xFF, 0xD8
	if q75[0] != 0xFF || q75[1] != 0xD8 {
		t.Errorf("output does not have JPEG magic bytes")
	}

	// Quality 45 should generally be smaller than Quality 75
	q45, err := compressImageToJPEG(pngData, 45)
	if err != nil {
		t.Fatalf("compressImageToJPEG(45) failed: %v", err)
	}
	if len(q45) > len(q75) {
		t.Errorf("expected quality 45 (%d bytes) <= quality 75 (%d bytes)", len(q45), len(q75))
	}
}
