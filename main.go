package main

import (
	"crypto/tls"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu"
	"github.com/schollz/progressbar/v3"
)

var title string
var tempDownloadFolder string
var insecure bool
var keepDownloadFolder bool
var donwloadThreads int
var downloadRetries int
var downloadRetryDelay time.Duration
var chunkSize uint
var quality int
var preset string
var noPrompt bool
var compressFile string
var outputFileOverride string
var botMode bool
var botToken string
var allowedUsers string

type flipbook struct {
	URL       *url.URL
	title     string
	pageCount int
	pageURLs  []string
}

type downloadOptions struct {
	threads        int
	retries        int
	retryDelay     time.Duration
	quality        int
	samplePageData []byte
}

func init() {
	flag.Usage = printUsage
	flag.StringVar(&tempDownloadFolder, "temp-download-folder", "", "Specifies the name of the temporary download folder")
	flag.StringVar(&title, "title", "", "Specifies the name of the generated PDF document (uses book title if not specified)")
	flag.BoolVar(&insecure, "insecure", false, "Skip certificate validation")
	flag.BoolVar(&keepDownloadFolder, "keep-download-folder", false, "Keep the temporary download folder instead of deleting it after completion")
	flag.IntVar(&donwloadThreads, "threads", 1, "Number of parallel download processes")
	flag.IntVar(&downloadRetries, "retries", 1, "Number of download retries")
	flag.DurationVar(&downloadRetryDelay, "waitretry", time.Second, "Wait time between download retries")
	flag.UintVar(&chunkSize, "chunksize", 10, "Amount of images converted at once. Higher amount amount will end in less write actions but more memory usage")
	flag.IntVar(&quality, "quality", 0, "JPEG compression quality (1-100, 100=original uncompressed, default: interactive prompt)")
	flag.StringVar(&preset, "preset", "", "Quality preset: original, recommended, compact, small")
	flag.BoolVar(&noPrompt, "yes", false, "Skip interactive prompts and use recommended defaults")
	flag.BoolVar(&noPrompt, "no-prompt", false, "Skip interactive prompts and use recommended defaults")
	flag.StringVar(&compressFile, "compress", "", "Compress an existing PDF document instead of downloading")
	flag.StringVar(&outputFileOverride, "output", "", "Output filename for the compressed PDF")
	flag.BoolVar(&botMode, "bot", false, "Run as a Telegram bot")
	flag.StringVar(&botToken, "token", "", "Telegram bot token (or set TELEGRAM_BOT_TOKEN env var)")
	flag.StringVar(&allowedUsers, "allowed-users", "", "Comma-separated list of allowed Telegram user IDs (or set ALLOWED_USER_IDS env var)")
}

func main() {
	flag.Parse()
	run()
}

func run() {
	if botMode || os.Getenv("TELEGRAM_BOT_MODE") == "1" {
		token := botToken
		if token == "" {
			token = os.Getenv("TELEGRAM_BOT_TOKEN")
		}
		if token == "" {
			log.Fatal("error: telegram bot token is required. Pass -token or set TELEGRAM_BOT_TOKEN env var")
		}

		userList := allowedUsers
		if userList == "" {
			userList = os.Getenv("ALLOWED_USER_IDS")
		}
		users := parseAllowedUsers(userList)

		bot := newTelegramBot(token, users)
		if err := bot.Start(); err != nil {
			log.Fatalf("bot error: %v", err)
		}
		return
	}

	if compressFile != "" {
		compQuality := QualityRecommended
		if quality > 0 && quality <= 100 {
			compQuality = quality
		} else if pQuality, ok := parsePreset(preset); ok {
			compQuality = pQuality
		} else if isInteractiveTerminal() && !noPrompt {
			estimates := fallbackEstimates(100)
			compQuality = promptCompressionChoice(estimates)
		}

		err := compressPDFFile(compressFile, outputFileOverride, compQuality)
		if err != nil {
			log.Fatal(err)
		}
		return
	}

	if flag.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "error: no URL provided")
		printUsage()
		os.Exit(1)
	}

	anyflipURL, err := url.Parse(flag.Args()[0])
	if err != nil {
		log.Fatal(err)
	}

	if insecure {
		fmt.Println("You enabled insecure downloads. This disables security checks. Stay safe!")
		http.DefaultTransport.(*http.Transport).TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}

	fmt.Println("Preparing to download")
	flipbook, err := prepareDownload(anyflipURL)
	if err != nil {
		log.Fatal(err)
	}

	selectedQuality := QualityRecommended
	var sampleData []byte

	if quality > 0 && quality <= 100 {
		selectedQuality = quality
	} else if pQuality, ok := parsePreset(preset); ok {
		selectedQuality = pQuality
	} else if noPrompt || !isInteractiveTerminal() {
		selectedQuality = QualityRecommended
	} else {
		fmt.Printf("Analyzing book (%d pages)...\n", flipbook.pageCount)
		var estimates *SizeEstimates
		sampleData, estimates, err = flipbook.samplePage()
		if err != nil {
			estimates = fallbackEstimates(flipbook.pageCount)
		}
		selectedQuality = promptCompressionChoice(estimates)
	}

	if tempDownloadFolder == "" {
		tempDownloadFolder = flipbook.title
	}
	// Use absolute path to avoid Windows "path not found" errors
	tempDownloadFolder, err = filepath.Abs(tempDownloadFolder)
	if err != nil {
		log.Fatalf("invalid download path: %v", err)
	}
	outputFile := title + ".pdf"
	if outputFileOverride != "" {
		outputFile = outputFileOverride
	}

	err = flipbook.downloadImages(
		tempDownloadFolder,
		downloadOptions{
			threads:        donwloadThreads,
			retries:        downloadRetries,
			retryDelay:     downloadRetryDelay,
			quality:        selectedQuality,
			samplePageData: sampleData,
		},
	)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("Converting to pdf")
	err = createPDF(outputFile, tempDownloadFolder, int(chunkSize))
	if err != nil {
		log.Fatal(err)
	}

	if !keepDownloadFolder {
		os.RemoveAll(tempDownloadFolder)
	}

	if fi, err := os.Stat(outputFile); err == nil {
		fmt.Printf("\nDone! Saved to: %s (%s)\n", outputFile, formatBytes(fi.Size()))
	} else {
		fmt.Println("Done")
	}
}

func printUsage() {
	w := flag.CommandLine.Output()
	fmt.Fprintf(w, "Usage:\n")
	fmt.Fprintf(w, "  %s [OPTIONS] <url>\n", os.Args[0])
	fmt.Fprintf(w, "  %s -compress <file.pdf> [OPTIONS]\n", os.Args[0])
	fmt.Fprintf(w, "  %s -bot [OPTIONS]\n", os.Args[0])
	fmt.Fprintf(w, "\nOptions:\n")
	flag.PrintDefaults()
}

func createPDF(outputFile string, imageDir string, chunkSize int) error {
	outputFile = strings.ReplaceAll(outputFile, "'", "")
	outputFile = strings.ReplaceAll(outputFile, "\\", "")
	outputFile = strings.ReplaceAll(outputFile, ":", "")

	if _, err := os.Stat(outputFile); err == nil {
		fmt.Printf("Output file %s already exists\n", outputFile)
		return nil
	}

	files, err := os.ReadDir(imageDir)
	if err != nil {
		return err
	}

	var imagePaths []string

	for _, file := range files {
		if file.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(file.Name()))
		if ext == ".jpg" || ext == ".jpeg" || ext == ".png" || ext == ".webp" {
			imagePaths = append(imagePaths, filepath.Join(imageDir, file.Name()))
		}
	}

	if len(imagePaths) == 0 {
		return fmt.Errorf("no images found in path %s", imageDir)
	}

	sort.Strings(imagePaths)

	bar := progressbar.NewOptions(len(imagePaths),
		progressbar.OptionFullWidth(),
		progressbar.OptionSetPredictTime(false),
		progressbar.OptionShowCount(),
		progressbar.OptionSetDescription("Converting"),
	)
	defer bar.Close()

	impConf := pdfcpu.DefaultImportConfig()

	for i := 0; i < len(imagePaths); i += chunkSize {
		end := min(i+chunkSize, len(imagePaths))

		imagePathChunk := imagePaths[i:end]
		err = api.ImportImagesFile(imagePathChunk, outputFile, impConf, nil)
		bar.Add(min(chunkSize, len(imagePaths)-i))
	}

	fmt.Println()
	return err
}
