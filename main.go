package main

import (
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	qrcode "github.com/skip2/go-qrcode"
)

// Poll 投票结构
type Poll struct {
	ID          string         `json:"id"`
	Title       string         `json:"title"`
	Options     []string       `json:"options"`
	MultiSelect bool           `json:"multi_select"`
	MinChoices  int            `json:"min_choices"` // 最少选择数量，0表示无限制
	MaxChoices  int            `json:"max_choices"` // 最多选择数量，0表示无限制
	Votes       map[string]int `json:"votes"`       // option -> count
	VoterCount  int            `json:"voter_count"` // 投票人数
	CreatedAt   time.Time      `json:"created_at"`
}

// VoteRequest 投票请求
type VoteRequest struct {
	PollID  string   `json:"poll_id"`
	Options []string `json:"options"`
}

// PollStore 投票存储
type PollStore struct {
	mu    sync.RWMutex
	polls map[string]*Poll
}

func NewPollStore() *PollStore {
	return &PollStore{
		polls: make(map[string]*Poll),
	}
}

func (ps *PollStore) Create(title string, options []string, multiSelect bool, minChoices, maxChoices int) *Poll {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	poll := &Poll{
		ID:          uuid.New().String(),
		Title:       title,
		Options:     options,
		MultiSelect: multiSelect,
		MinChoices:  minChoices,
		MaxChoices:  maxChoices,
		Votes:       make(map[string]int),
		CreatedAt:   time.Now(),
	}

	// 初始化投票计数
	for _, opt := range options {
		poll.Votes[opt] = 0
	}

	ps.polls[poll.ID] = poll
	return poll
}

func (ps *PollStore) Get(id string) (*Poll, bool) {
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	poll, ok := ps.polls[id]
	return poll, ok
}

func (ps *PollStore) GetAll() []*Poll {
	ps.mu.RLock()
	defer ps.mu.RUnlock()

	polls := make([]*Poll, 0, len(ps.polls))
	for _, poll := range ps.polls {
		polls = append(polls, poll)
	}
	return polls
}

func (ps *PollStore) Delete(id string) error {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	if _, ok := ps.polls[id]; !ok {
		return fmt.Errorf("poll not found")
	}

	delete(ps.polls, id)
	return nil
}

func (ps *PollStore) AddVote(pollID string, options []string) error {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	poll, ok := ps.polls[pollID]
	if !ok {
		return fmt.Errorf("poll not found")
	}

	// 增加投票人数
	poll.VoterCount++

	// 增加每个选项的票数
	for _, opt := range options {
		if _, exists := poll.Votes[opt]; exists {
			poll.Votes[opt]++
		}
	}

	return nil
}

var store = NewPollStore()
var templates *template.Template

func init() {
	// 加载所有模板文件
	funcMap := template.FuncMap{
		"multiply": func(a, b interface{}) float64 {
			var af, bf float64
			switch v := a.(type) {
			case int:
				af = float64(v)
			case float64:
				af = v
			}
			switch v := b.(type) {
			case int:
				bf = float64(v)
			case float64:
				bf = v
			}
			return af * bf
		},
		"divide": func(a, b interface{}) float64 {
			var af, bf float64
			switch v := a.(type) {
			case int:
				af = float64(v)
			case float64:
				af = v
			}
			switch v := b.(type) {
			case int:
				bf = float64(v)
			case float64:
				bf = v
			}
			if bf == 0 {
				return 0
			}
			return af / bf
		},
	}
	templates = template.Must(template.New("").Funcs(funcMap).ParseGlob("templates/*.html"))
}

func main() {
	http.HandleFunc("/", indexHandler)
	http.HandleFunc("/create", createHandler)
	http.HandleFunc("/api/polls", apiPollsHandler)
	http.HandleFunc("/api/create-poll", apiCreatePollHandler)
	http.HandleFunc("/api/delete-poll/", apiDeletePollHandler)
	http.HandleFunc("/poll/", pollHandler)
	http.HandleFunc("/api/vote", apiVoteHandler)
	http.HandleFunc("/api/results/", apiResultsHandler)
	http.HandleFunc("/qrcode/", qrcodeHandler)

	port := ":8888"
	fmt.Printf("服务器启动在 http://localhost%s\n", port)
	log.Fatal(http.ListenAndServe(port, nil))
}

func indexHandler(w http.ResponseWriter, r *http.Request) {
	// 只有根路径才显示首页，其他路径返回404
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := templates.ExecuteTemplate(w, "index.html", nil); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func apiPollsHandler(w http.ResponseWriter, r *http.Request) {
	polls := store.GetAll()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"polls":   polls,
	})
}

func apiDeletePollHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete && r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	pollID := r.URL.Path[len("/api/delete-poll/"):]
	if pollID == "" {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "Poll ID is required",
		})
		return
	}

	if err := store.Delete(pollID); err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Poll deleted successfully",
	})
}

func createHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := templates.ExecuteTemplate(w, "create.html", nil); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func apiCreatePollHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Title       string   `json:"title"`
		Options     []string `json:"options"`
		MultiSelect bool     `json:"multi_select"`
		MinChoices  int      `json:"min_choices"`
		MaxChoices  int      `json:"max_choices"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "Invalid request",
		})
		return
	}

	poll := store.Create(req.Title, req.Options, req.MultiSelect, req.MinChoices, req.MaxChoices)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"poll_id": poll.ID,
	})
}

func pollHandler(w http.ResponseWriter, r *http.Request) {
	pollID := r.URL.Path[len("/poll/"):]
	poll, ok := store.Get(pollID)
	if !ok {
		http.Error(w, "Poll not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := templates.ExecuteTemplate(w, "poll.html", poll); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func apiVoteHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req VoteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "Invalid request",
		})
		return
	}

	if err := store.AddVote(req.PollID, req.Options); err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
	})
}

func apiResultsHandler(w http.ResponseWriter, r *http.Request) {
	pollID := r.URL.Path[len("/api/results/"):]
	poll, ok := store.Get(pollID)
	if !ok {
		http.Error(w, "Poll not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := templates.ExecuteTemplate(w, "results.html", poll); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func qrcodeHandler(w http.ResponseWriter, r *http.Request) {
	pollID := r.URL.Path[len("/qrcode/"):]

	// 生成投票页面 URL
	pollURL := fmt.Sprintf("http://localhost:8888/poll/%s", pollID)

	// 生成二维码
	qr, err := qrcode.Encode(pollURL, qrcode.Medium, 256)
	if err != nil {
		http.Error(w, "Failed to generate QR code", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "image/png")
	w.Write(qr)
}
