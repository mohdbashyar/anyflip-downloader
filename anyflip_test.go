package main

import (
	"net/url"
	"path"
	"testing"
)

func TestBookAssetBaseURLPreservesProviderHost(t *testing.T) {
	for _, provider := range []string{"online.anyflip.com", "online.fliphtml5.com"} {
		bookURL, err := url.Parse("https://" + provider + "/user/book")
		if err != nil {
			t.Fatal(err)
		}

		assetURL := bookAssetBaseURL(bookURL)
		assetURL.Path = path.Join(bookURL.Path, "files", "mobile", "1.jpg")

		want := "https://" + provider + "/user/book/files/mobile/1.jpg"
		if assetURL.String() != want {
			t.Errorf("asset URL = %q, want %q", assetURL.String(), want)
		}
	}
}

func TestIsFlipHTML5URL(t *testing.T) {
	flipHTML5URL, err := url.Parse("https://online.fliphtml5.com/mvgld/qxxu/")
	if err != nil {
		t.Fatal(err)
	}
	if !isFlipHTML5URL(flipHTML5URL) {
		t.Fatal("expected FlipHTML5 URL to be recognized")
	}

	anyflipURL, err := url.Parse("https://online.anyflip.com/mvgld/qxxu/")
	if err != nil {
		t.Fatal(err)
	}
	if isFlipHTML5URL(anyflipURL) {
		t.Fatal("did not expect AnyFlip URL to be recognized as FlipHTML5")
	}
}
