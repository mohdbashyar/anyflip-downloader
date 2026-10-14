package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Telegram API Types
type TGUser struct {
	ID        int64  `json:"id"`
	FirstName string `json:"first_name"`
	Username  string `json:"username"`
}

type TGChat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

type TGMessage struct {
	MessageID int     `json:"message_id"`
	From      *TGUser `json:"from"`
	Chat      TGChat  `json:"chat"`
	Text      string  `json:"text"`
}

type TGInlineKeyboardButton struct {
	Text         string `json:"text"`
	CallbackData string `json:"callback_data"`
}

type TGInlineKeyboardMarkup struct {
	InlineKeyboard [][]TGInlineKeyboardButton `json:"inline_keyboard"`
}

type TGCallbackQuery struct {
	ID      string     `json:"id"`
	From    TGUser     `json:"from"`
	Message *TGMessage `json:"message"`
	Data    string     `json:"data"`
}

type TGUpdate struct {
	UpdateID      int              `json:"update_id"`
	Message       *TGMessage       `json:"message"`
	CallbackQuery *TGCallbackQuery `json:"callback_query"`
}

type TGResponse[T any] struct {
	OK          bool   `json:"ok"`
	Result      T      `json:"result"`
	Description string `json:"description"`
}

type pendingBookRequest struct {
	ID         string
	URL        *url.URL
	Flipbook   *flipbook
	Estimates  *SizeEstimates
	SampleData []byte
	CreatedAt  time.Time
}

type TelegramBot struct {
	token        string
	apiBase      string
	allowedUsers []int64
	client       *http.Client
	pending      map[string]*pendingBookRequest
	pendingMu    sync.RWMutex
}

func newTelegramBot(token string, allowedUsers []int64) *TelegramBot {
	return &TelegramBot{
		token:        token,
		apiBase:      fmt.Sprintf("https://api.telegram.org/bot%s", token),
		allowedUsers: allowedUsers,
		client:       &http.Client{Timeout: 120 * time.Second},
		pending:      make(map[string]*pendingBookRequest),
	}
}

func (b *TelegramBot) isAuthorized(userID int64) bool {
	if len(b.allowedUsers) == 0 {
		return true // open / no restriction
	}
	for _, id := range b.allowedUsers {
		if id == userID {
			return true
		}
	}
	return false
}

func (b *TelegramBot) getMe() (*TGUser, error) {
	url := fmt.Sprintf("%s/getMe", b.apiBase)
	resp, err := b.client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var res TGResponse[TGUser]
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, err
	}
	if !res.OK {
		return nil, fmt.Errorf("telegram API error: %s", res.Description)
	}
	return &res.Result, nil
}

func (b *TelegramBot) getUpdates(offset int, timeoutSec int) ([]TGUpdate, error) {
	u := fmt.Sprintf("%s/getUpdates?offset=%d&timeout=%d", b.apiBase, offset, timeoutSec)
	resp, err := b.client.Get(u)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var res TGResponse[[]TGUpdate]
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, err
	}
	if !res.OK {
		return nil, fmt.Errorf("telegram API error: %s", res.Description)
	}
	return res.Result, nil
}

func (b *TelegramBot) sendMessage(chatID int64, text string, replyMarkup *TGInlineKeyboardMarkup) (*TGMessage, error) {
	payload := map[string]any{
		"chat_id":    chatID,
		"text":       text,
		"parse_mode": "Markdown",
	}
	if replyMarkup != nil {
		payload["reply_markup"] = replyMarkup
	}

	body, _ := json.Marshal(payload)
	u := fmt.Sprintf("%s/sendMessage", b.apiBase)
	resp, err := b.client.Post(u, "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var res TGResponse[TGMessage]
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, err
	}
	return &res.Result, nil
}

func (b *TelegramBot) editMessageText(chatID int64, messageID int, text string, replyMarkup *TGInlineKeyboardMarkup) error {
	payload := map[string]any{
		"chat_id":    chatID,
		"message_id": messageID,
		"text":       text,
		"parse_mode": "Markdown",
	}
	if replyMarkup != nil {
		payload["reply_markup"] = replyMarkup
	}

	body, _ := json.Marshal(payload)
	u := fmt.Sprintf("%s/editMessageText", b.apiBase)
	resp, err := b.client.Post(u, "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}

func (b *TelegramBot) answerCallbackQuery(queryID string, text string) {
	payload := map[string]any{
		"callback_query_id": queryID,
		"text":              text,
	}
	body, _ := json.Marshal(payload)
	u := fmt.Sprintf("%s/answerCallbackQuery", b.apiBase)
	resp, err := b.client.Post(u, "application/json", bytes.NewReader(body))
	if err == nil {
		resp.Body.Close()
	}
}

func (b *TelegramBot) sendChatAction(chatID int64, action string) {
	payload := map[string]any{
		"chat_id": chatID,
		"action":  action,
	}
	body, _ := json.Marshal(payload)
	u := fmt.Sprintf("%s/sendChatAction", b.apiBase)
	resp, err := b.client.Post(u, "application/json", bytes.NewReader(body))
	if err == nil {
		resp.Body.Close()
	}
}

func (b *TelegramBot) sendDocument(chatID int64, filePath, caption string) error {
	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	_ = writer.WriteField("chat_id", strconv.FormatInt(chatID, 10))
	if caption != "" {
		_ = writer.WriteField("caption", caption)
	}

	part, err := writer.CreateFormFile("document", filepath.Base(filePath))
	if err != nil {
		return err
	}
	if _, err = io.Copy(part, file); err != nil {
		return err
	}
	if err = writer.Close(); err != nil {
		return err
	}

	u := fmt.Sprintf("%s/sendDocument", b.apiBase)
	req, err := http.NewRequest("POST", u, &body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := b.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var res TGResponse[TGMessage]
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return err
	}
	if !res.OK {
		return fmt.Errorf("telegram API error: %s", res.Description)
	}
	return nil
}

func (b *TelegramBot) cleanOldPending() {
	b.pendingMu.Lock()
	defer b.pendingMu.Unlock()
	cutoff := time.Now().Add(-1 * time.Hour)
	for id, req := range b.pending {
		if req.CreatedAt.Before(cutoff) {
			delete(b.pending, id)
		}
	}
}

func findBookURL(text string) *url.URL {
	words := strings.Fields(text)
	for _, w := range words {
		w = strings.Trim(w, "<>()[]\"',")
		if strings.Contains(w, "anyflip.com") || strings.Contains(w, "fliphtml5.com") {
			if !strings.HasPrefix(w, "http://") && !strings.HasPrefix(w, "https://") {
				w = "https://" + w
			}
			if u, err := url.Parse(w); err == nil && u.Host != "" {
				return u
			}
		}
	}
	return nil
}

// Start launches the Telegram bot polling loop
func (b *TelegramBot) Start() error {
	me, err := b.getMe()
	if err != nil {
		return fmt.Errorf("failed to connect to Telegram: %w", err)
	}

	log.Printf("🤖 Bot started successfully! Logged in as @%s (ID: %d)", me.Username, me.ID)
	if len(b.allowedUsers) > 0 {
		log.Printf("🔒 Access locked to %d authorized user(s)", len(b.allowedUsers))
	} else {
		log.Printf("⚠️ WARNING: No allowed-users specified! Bot is accessible to everyone.")
	}

	// Start optional HTTP health-check server if PORT is provided (for cloud hosts like Render)
	port := os.Getenv("PORT")
	if port != "" {
		go func() {
			mux := http.NewServeMux()
			mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				fmt.Fprintf(w, "AnyFlip Downloader Telegram Bot is online!\n")
			})
			mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				fmt.Fprintf(w, "OK\n")
			})
			log.Printf("🌐 HTTP server listening on port %s (health check ready)", port)
			if err := http.ListenAndServe(":"+port, mux); err != nil {
				log.Printf("HTTP server warning: %v", err)
			}
		}()
	}

	// Clean up old pending requests every 10 minutes
	go func() {
		ticker := time.NewTicker(10 * time.Minute)
		for range ticker.C {
			b.cleanOldPending()
		}
	}()

	offset := 0
	for {
		updates, err := b.getUpdates(offset, 30)
		if err != nil {
			log.Printf("Error fetching updates: %v. Retrying in 3s...", err)
			time.Sleep(3 * time.Second)
			continue
		}

		for _, update := range updates {
			if update.UpdateID >= offset {
				offset = update.UpdateID + 1
			}

			if update.Message != nil {
				go b.handleMessage(update.Message)
			} else if update.CallbackQuery != nil {
				go b.handleCallback(update.CallbackQuery)
			}
		}
	}
}

func (b *TelegramBot) handleMessage(msg *TGMessage) {
	if msg.From == nil {
		return
	}

	if !b.isAuthorized(msg.From.ID) {
		log.Printf("Blocked unauthorized user: %d (@%s)", msg.From.ID, msg.From.Username)
		b.sendMessage(msg.Chat.ID, "⛔ *Unauthorized*\n\nThis bot is private. Your user ID is not in the whitelist.", nil)
		return
	}

	text := strings.TrimSpace(msg.Text)
	if text == "/start" || text == "/help" {
		helpText := "👋 *Welcome to AnyFlip & FlipHTML5 Downloader Bot!*\n\n" +
			"Send me any book link from:\n" +
			"• `online.anyflip.com`\n" +
			"• `online.fliphtml5.com`\n\n" +
			"I will analyze the book, calculate file sizes, and let you choose your preferred quality before creating the PDF!"
		b.sendMessage(msg.Chat.ID, helpText, nil)
		return
	}

	bookURL := findBookURL(text)
	if bookURL == nil {
		b.sendMessage(msg.Chat.ID, "❌ Please send a valid AnyFlip or FlipHTML5 book URL.\n\nExample:\n`https://online.anyflip.com/mvgld/qxxu/`", nil)
		return
	}

	statusMsg, _ := b.sendMessage(msg.Chat.ID, "🔍 *Analyzing book...*", nil)

	go func() {
		flipbook, err := prepareDownload(bookURL)
		if err != nil {
			errMsg := fmt.Sprintf("❌ *Failed to fetch book*: %s", err.Error())
			if statusMsg != nil {
				b.editMessageText(msg.Chat.ID, statusMsg.MessageID, errMsg, nil)
			} else {
				b.sendMessage(msg.Chat.ID, errMsg, nil)
			}
			return
		}

		sampleData, estimates, err := flipbook.samplePage()
		if err != nil {
			estimates = fallbackEstimates(flipbook.pageCount)
		}

		// Store pending request
		reqID := fmt.Sprintf("%d", time.Now().UnixNano())
		b.pendingMu.Lock()
		b.pending[reqID] = &pendingBookRequest{
			ID:         reqID,
			URL:        bookURL,
			Flipbook:   flipbook,
			Estimates:  estimates,
			SampleData: sampleData,
			CreatedAt:  time.Now(),
		}
		b.pendingMu.Unlock()

		caption := fmt.Sprintf("📖 *%s*\n📄 Total Pages: *%d*\n\nChoose PDF download quality:",
			escapeMarkdown(flipbook.title), flipbook.pageCount)

		keyboard := &TGInlineKeyboardMarkup{
			InlineKeyboard: [][]TGInlineKeyboardButton{
				{
					{
						Text:         fmt.Sprintf("🟢 Recommended (~%s) - 75%%", formatBytes(estimates.RecommendedBytes)),
						CallbackData: fmt.Sprintf("dl:%s:75", reqID),
					},
				},
				{
					{
						Text:         fmt.Sprintf("🟡 Compact (~%s) - 60%%", formatBytes(estimates.CompactBytes)),
						CallbackData: fmt.Sprintf("dl:%s:60", reqID),
					},
				},
				{
					{
						Text:         fmt.Sprintf("🔵 Small (~%s) - 45%%", formatBytes(estimates.SmallBytes)),
						CallbackData: fmt.Sprintf("dl:%s:45", reqID),
					},
				},
				{
					{
						Text:         fmt.Sprintf("⚪ Original (~%s) - 100%%", formatBytes(estimates.OriginalBytes)),
						CallbackData: fmt.Sprintf("dl:%s:100", reqID),
					},
				},
			},
		}

		if statusMsg != nil {
			b.editMessageText(msg.Chat.ID, statusMsg.MessageID, caption, keyboard)
		} else {
			b.sendMessage(msg.Chat.ID, caption, keyboard)
		}
	}()
}

func (b *TelegramBot) handleCallback(cb *TGCallbackQuery) {
	if !b.isAuthorized(cb.From.ID) {
		b.answerCallbackQuery(cb.ID, "Unauthorized")
		return
	}

	data := cb.Data
	if !strings.HasPrefix(data, "dl:") {
		return
	}

	parts := strings.Split(data, ":")
	if len(parts) != 3 {
		return
	}

	reqID := parts[1]
	quality, _ := strconv.Atoi(parts[2])

	b.pendingMu.RLock()
	req, ok := b.pending[reqID]
	b.pendingMu.RUnlock()

	if !ok {
		b.answerCallbackQuery(cb.ID, "Request expired. Please re-send the link.")
		return
	}

	b.answerCallbackQuery(cb.ID, "Starting download...")

	chatID := cb.Message.Chat.ID
	msgID := cb.Message.MessageID

	go func() {
		statusText := fmt.Sprintf("⏳ *Downloading & converting*:\n_%s_\n(Pages: %d, Quality: %d%%)...",
			escapeMarkdown(req.Flipbook.title), req.Flipbook.pageCount, quality)
		b.editMessageText(chatID, msgID, statusText, nil)

		tmpDir, err := os.MkdirTemp("", "tg_anyflip_*")
		if err != nil {
			b.editMessageText(chatID, msgID, fmt.Sprintf("❌ Error: %v", err), nil)
			return
		}
		defer os.RemoveAll(tmpDir)

		outputPDF := filepath.Join(tmpDir, req.Flipbook.title+".pdf")

		// Download images
		err = req.Flipbook.downloadImages(
			tmpDir,
			downloadOptions{
				threads:        donwloadThreads,
				retries:        downloadRetries,
				retryDelay:     downloadRetryDelay,
				quality:        quality,
				samplePageData: req.SampleData,
			},
		)
		if err != nil {
			b.editMessageText(chatID, msgID, fmt.Sprintf("❌ Download error: %v", err), nil)
			return
		}

		b.editMessageText(chatID, msgID, "📑 *Building PDF document...*", nil)
		err = createPDF(outputPDF, tmpDir, int(chunkSize))
		if err != nil {
			b.editMessageText(chatID, msgID, fmt.Sprintf("❌ PDF creation error: %v", err), nil)
			return
		}

		fi, err := os.Stat(outputPDF)
		if err != nil {
			b.editMessageText(chatID, msgID, fmt.Sprintf("❌ File error: %v", err), nil)
			return
		}

		// Check Telegram 50 MB limit
		if fi.Size() > 50*1024*1024 {
			errMsg := fmt.Sprintf("⚠️ *PDF size is %s, which exceeds Telegram's 50 MB limit!*\n\nPlease try again and select 'Compact' (JPEG 60) or 'Small' (JPEG 45).", formatBytes(fi.Size()))
			b.editMessageText(chatID, msgID, errMsg, nil)
			return
		}

		b.editMessageText(chatID, msgID, fmt.Sprintf("📤 *Uploading PDF (%s) to Telegram...*", formatBytes(fi.Size())), nil)
		b.sendChatAction(chatID, "upload_document")

		caption := fmt.Sprintf("📖 *%s*\n📄 %d pages • %s", req.Flipbook.title, req.Flipbook.pageCount, formatBytes(fi.Size()))
		err = b.sendDocument(chatID, outputPDF, caption)
		if err != nil {
			b.editMessageText(chatID, msgID, fmt.Sprintf("❌ Upload failed: %v", err), nil)
			return
		}

		b.editMessageText(chatID, msgID, fmt.Sprintf("✅ *Sent!* (%s)", formatBytes(fi.Size())), nil)
	}()
}

func escapeMarkdown(s string) string {
	replacer := strings.NewReplacer(
		"_", "\\_",
		"*", "\\*",
		"[", "\\[",
		"]", "\\]",
		"`", "\\`",
	)
	return replacer.Replace(s)
}

func parseAllowedUsers(raw string) []int64 {
	if raw == "" {
		return nil
	}
	var users []int64
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if id, err := strconv.ParseInt(part, 10, 64); err == nil {
			users = append(users, id)
		}
	}
	return users
}
