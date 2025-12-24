package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	qrcode "github.com/skip2/go-qrcode"
	_ "modernc.org/sqlite"
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
	db *sql.DB
}

func NewPollStore(dbPath string) (*PollStore, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}

	// 创建表
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS polls (
			id TEXT PRIMARY KEY,
			title TEXT NOT NULL,
			options TEXT NOT NULL,
			multi_select INTEGER NOT NULL,
			min_choices INTEGER NOT NULL,
			max_choices INTEGER NOT NULL,
			voter_count INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL
		);

		CREATE TABLE IF NOT EXISTS votes (
			poll_id TEXT NOT NULL,
			option_name TEXT NOT NULL,
			vote_count INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (poll_id, option_name),
			FOREIGN KEY (poll_id) REFERENCES polls(id) ON DELETE CASCADE
		);
	`)
	if err != nil {
		return nil, err
	}

	return &PollStore{db: db}, nil
}

func (ps *PollStore) Close() error {
	return ps.db.Close()
}

func (ps *PollStore) Create(title string, options []string, multiSelect bool, minChoices, maxChoices int) (*Poll, error) {
	poll := &Poll{
		ID:          uuid.New().String(),
		Title:       title,
		Options:     options,
		MultiSelect: multiSelect,
		MinChoices:  minChoices,
		MaxChoices:  maxChoices,
		Votes:       make(map[string]int),
		VoterCount:  0,
		CreatedAt:   time.Now(),
	}

	// 开始事务
	tx, err := ps.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	// 插入投票
	multiSelectInt := 0
	if multiSelect {
		multiSelectInt = 1
	}
	_, err = tx.Exec(`
		INSERT INTO polls (id, title, options, multi_select, min_choices, max_choices, voter_count, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, poll.ID, poll.Title, strings.Join(options, "|||"), multiSelectInt, minChoices, maxChoices, 0, poll.CreatedAt)
	if err != nil {
		return nil, err
	}

	// 初始化投票选项
	for _, opt := range options {
		_, err = tx.Exec(`
			INSERT INTO votes (poll_id, option_name, vote_count)
			VALUES (?, ?, 0)
		`, poll.ID, opt)
		if err != nil {
			return nil, err
		}
		poll.Votes[opt] = 0
	}

	if err = tx.Commit(); err != nil {
		return nil, err
	}

	return poll, nil
}

func (ps *PollStore) Get(id string) (*Poll, error) {
	var poll Poll
	var optionsStr string
	var multiSelectInt int
	var createdAtStr string

	err := ps.db.QueryRow(`
		SELECT id, title, options, multi_select, min_choices, max_choices, voter_count, created_at
		FROM polls
		WHERE id = ?
	`, id).Scan(&poll.ID, &poll.Title, &optionsStr, &multiSelectInt, &poll.MinChoices, &poll.MaxChoices, &poll.VoterCount, &createdAtStr)
	if err != nil {
		return nil, err
	}

	poll.MultiSelect = multiSelectInt == 1
	poll.Options = strings.Split(optionsStr, "|||")
	poll.CreatedAt, _ = time.Parse("2006-01-02 15:04:05.999999999-07:00", createdAtStr)

	// 获取投票数据
	poll.Votes = make(map[string]int)
	rows, err := ps.db.Query(`
		SELECT option_name, vote_count
		FROM votes
		WHERE poll_id = ?
	`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var optionName string
		var voteCount int
		if err := rows.Scan(&optionName, &voteCount); err != nil {
			return nil, err
		}
		poll.Votes[optionName] = voteCount
	}

	return &poll, nil
}

func (ps *PollStore) GetAll() ([]*Poll, error) {
	rows, err := ps.db.Query(`
		SELECT id, title, options, multi_select, min_choices, max_choices, voter_count, created_at
		FROM polls
		ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var polls []*Poll
	for rows.Next() {
		var poll Poll
		var optionsStr string
		var multiSelectInt int
		var createdAtStr string

		err := rows.Scan(&poll.ID, &poll.Title, &optionsStr, &multiSelectInt, &poll.MinChoices, &poll.MaxChoices, &poll.VoterCount, &createdAtStr)
		if err != nil {
			return nil, err
		}

		poll.MultiSelect = multiSelectInt == 1
		poll.Options = strings.Split(optionsStr, "|||")
		poll.CreatedAt, _ = time.Parse("2006-01-02 15:04:05.999999999-07:00", createdAtStr)

		// 获取投票数据
		poll.Votes = make(map[string]int)
		voteRows, err := ps.db.Query(`
			SELECT option_name, vote_count
			FROM votes
			WHERE poll_id = ?
		`, poll.ID)
		if err != nil {
			return nil, err
		}

		for voteRows.Next() {
			var optionName string
			var voteCount int
			if err := voteRows.Scan(&optionName, &voteCount); err != nil {
				voteRows.Close()
				return nil, err
			}
			poll.Votes[optionName] = voteCount
		}
		voteRows.Close()

		polls = append(polls, &poll)
	}

	return polls, nil
}

func (ps *PollStore) Delete(id string) error {
	result, err := ps.db.Exec(`DELETE FROM polls WHERE id = ?`, id)
	if err != nil {
		return err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}

	if rowsAffected == 0 {
		return fmt.Errorf("poll not found")
	}

	return nil
}

func (ps *PollStore) AddVote(pollID string, options []string) error {
	tx, err := ps.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// 检查投票是否存在
	var exists int
	err = tx.QueryRow(`SELECT COUNT(*) FROM polls WHERE id = ?`, pollID).Scan(&exists)
	if err != nil {
		return err
	}
	if exists == 0 {
		return fmt.Errorf("poll not found")
	}

	// 增加投票人数
	_, err = tx.Exec(`UPDATE polls SET voter_count = voter_count + 1 WHERE id = ?`, pollID)
	if err != nil {
		return err
	}

	// 增加每个选项的票数
	for _, opt := range options {
		_, err = tx.Exec(`
			UPDATE votes
			SET vote_count = vote_count + 1
			WHERE poll_id = ? AND option_name = ?
		`, pollID, opt)
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}

var store *PollStore
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
	var err error
	store, err = NewPollStore("data/toupiao.db")
	if err != nil {
		log.Fatal("初始化数据库失败:", err)
	}
	defer store.Close()

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
	polls, err := store.GetAll()
	if err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

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

	poll, err := store.Create(req.Title, req.Options, req.MultiSelect, req.MinChoices, req.MaxChoices)
	if err != nil {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"poll_id": poll.ID,
	})
}

func pollHandler(w http.ResponseWriter, r *http.Request) {
	pollID := r.URL.Path[len("/poll/"):]
	poll, err := store.Get(pollID)
	if err != nil {
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
	poll, err := store.Get(pollID)
	if err != nil {
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
	pollURL := fmt.Sprintf("https://tp.starpix.cn/poll/%s", pollID)

	// 生成二维码
	qr, err := qrcode.Encode(pollURL, qrcode.Medium, 256)
	if err != nil {
		http.Error(w, "Failed to generate QR code", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "image/png")
	w.Write(qr)
}
