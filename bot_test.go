package main

import (
	"reflect"
	"testing"
)

func TestParseAllowedUsers(t *testing.T) {
	tests := []struct {
		raw  string
		want []int64
	}{
		{"", nil},
		{"123456", []int64{123456}},
		{"123, 456, 789", []int64{123, 456, 789}},
		{"123,invalid,456", []int64{123, 456}},
	}

	for _, tc := range tests {
		got := parseAllowedUsers(tc.raw)
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("parseAllowedUsers(%q) = %v, want %v", tc.raw, got, tc.want)
		}
	}
}

func TestFindBookURL(t *testing.T) {
	tests := []struct {
		input   string
		wantURL string
		wantNil bool
	}{
		{
			input:   "https://online.anyflip.com/mvgld/qxxu/",
			wantURL: "https://online.anyflip.com/mvgld/qxxu/",
		},
		{
			input:   "Please download <https://online.fliphtml5.com/mvgld/qxxu/> for me",
			wantURL: "https://online.fliphtml5.com/mvgld/qxxu/",
		},
		{
			input:   "online.anyflip.com/abc/def",
			wantURL: "https://online.anyflip.com/abc/def",
		},
		{
			input:   "Hello bot, how are you doing today?",
			wantNil: true,
		},
	}

	for _, tc := range tests {
		u := findBookURL(tc.input)
		if tc.wantNil {
			if u != nil {
				t.Errorf("findBookURL(%q) = %v, want nil", tc.input, u)
			}
		} else {
			if u == nil || u.String() != tc.wantURL {
				t.Errorf("findBookURL(%q) = %v, want %s", tc.input, u, tc.wantURL)
			}
		}
	}
}

func TestIsAuthorized(t *testing.T) {
	botOpen := newTelegramBot("token", nil)
	if !botOpen.isAuthorized(12345) {
		t.Errorf("expected open bot to authorize any user")
	}

	botLocked := newTelegramBot("token", []int64{100, 200})
	if !botLocked.isAuthorized(100) {
		t.Errorf("expected user 100 to be authorized")
	}
	if !botLocked.isAuthorized(200) {
		t.Errorf("expected user 200 to be authorized")
	}
	if botLocked.isAuthorized(999) {
		t.Errorf("expected user 999 to be rejected")
	}
}

func TestEscapeMarkdown(t *testing.T) {
	input := "Book_Title [Vol 1] *Special* `Edition`"
	got := escapeMarkdown(input)
	want := "Book\\_Title \\[Vol 1\\] \\*Special\\* \\`Edition\\`"
	if got != want {
		t.Errorf("escapeMarkdown(%q) = %q, want %q", input, got, want)
	}
}
