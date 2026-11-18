package game

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Record 是一条关卡成绩（a=me 的 records 元素）；best_steps 是该关三星最小步数。
type Record struct {
	Level     int `json:"level"`
	Stars     int `json:"stars"`
	BestSteps int `json:"best_steps"`
	Clears    int `json:"clears"`
	Fails     int `json:"fails"`
}

// DailyRecord 是一条每日挑战成绩。
type DailyRecord struct {
	St    int `json:"st"`
	Steps int `json:"steps"`
}

// EndlessMode 是无尽模式成绩。
type EndlessMode struct {
	Best     int    `json:"best"`
	Week     string `json:"week"`
	WeekBest int    `json:"weekBest"`
}

// MatchMode 是消消喵成绩。
type MatchMode struct {
	Best int `json:"best"`
}

// SudokuMode 是数独喵成绩。
type SudokuMode struct {
	Best  int         `json:"best"`
	Stars map[int]int `json:"stars"`
}

// Modes 是 a=me 返回的模式成绩（结构化）。
type Modes struct {
	Daily   map[string]DailyRecord `json:"daily"`
	Fish    map[int]int            `json:"fish"`
	Endless EndlessMode            `json:"endless"`
	Match   MatchMode              `json:"match"`
	Sudoku  SudokuMode             `json:"sudoku"`
}

// UnmarshalJSON 自定义解析：fish 与 sudoku.stars 的键是数字字符串，统一转成 map[int]int；
// 非数字键忽略，避免单条脏数据导致整包解析失败。
func (m *Modes) UnmarshalJSON(data []byte) error {
	var raw struct {
		Daily   map[string]DailyRecord `json:"daily"`
		Fish    map[string]int         `json:"fish"`
		Endless EndlessMode            `json:"endless"`
		Match   MatchMode              `json:"match"`
		Sudoku  struct {
			Best  int            `json:"best"`
			Stars map[string]int `json:"stars"`
		} `json:"sudoku"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	parsed := &Modes{
		Daily:   raw.Daily,
		Fish:    make(map[int]int, len(raw.Fish)),
		Endless: raw.Endless,
		Match:   raw.Match,
		Sudoku:  SudokuMode{Best: raw.Sudoku.Best, Stars: make(map[int]int, len(raw.Sudoku.Stars))},
	}
	if parsed.Daily == nil {
		parsed.Daily = map[string]DailyRecord{}
	}
	for k, v := range raw.Fish {
		if level, err := strconv.Atoi(k); err == nil {
			parsed.Fish[level] = v
		}
	}
	for k, v := range raw.Sudoku.Stars {
		if level, err := strconv.Atoi(k); err == nil {
			parsed.Sudoku.Stars[level] = v
		}
	}
	*m = *parsed
	return nil
}

// User 是 a=me 返回的账号信息（含关卡记录、模式成绩与道具）。
type User struct {
	ID           int              `json:"id"`
	Nickname     string           `json:"nickname"`
	Avatar       int              `json:"avatar"`
	WechatAvatar string           `json:"wechatAvatar"`
	Stars        int              `json:"stars"`
	Cleared      int              `json:"cleared"`
	Top          int              `json:"top"`
	Three        int              `json:"three"`
	Fish         map[int]int      `json:"fish"`
	Endless      int              `json:"endless"`
	SudokuBest   int              `json:"sudokuBest"`
	SudokuStars  map[int]int      `json:"sudokuStars"`
	MatchBest    int              `json:"matchBest"`
	Items        map[string]int64 `json:"items"`
	ResetAt      int              `json:"resetAt"`
	Skin         string           `json:"skin"`
	// Records 是全部关卡成绩（含 best_steps），可用作三星最小步数缓存。
	Records []Record `json:"records,omitempty"`
	// Modes 是结构化模式成绩（daily / fish / endless / match / sudoku）。
	Modes *Modes `json:"modes,omitempty"`
}

// newUser 返回带空 map 的 User，保证序列化时不会出现 null。
func newUser(u map[string]any) *User {
	return &User{
		ID:           numOf(u["id"]),
		Nickname:     strOf(u["nickname"]),
		Avatar:       numOf(u["avatar"]),
		WechatAvatar: strOf(u["wechatAvatar"]),
		Stars:        numOf(u["stars"]),
		Cleared:      numOf(u["cleared"]),
		Top:          numOf(u["top"]),
		Three:        numOf(u["three"]),
		ResetAt:      numOf(u["resetAt"]),
		Skin:         strOf(u["skin"]),
		Fish:         map[int]int{},
		SudokuStars:  map[int]int{},
		Items:        map[string]int64{},
	}
}

// parseMe 解析 a=me 的 data 对象（user / records / modes / items）。
func parseMe(data map[string]any) *User {
	if data == nil {
		return nil
	}
	u := mapOf(data["user"])
	if u == nil {
		return nil
	}
	user := parseUser(u, mapOf(data["modes"]), mapOf(data["items"]))
	user.Records = parseRecords(data["records"])
	return user
}

// parseUser 解析 user 对象，并合并 modes / items。
// items 为 nil 时回退到 user.items；modes 为 nil 时回退到 user.modes。
func parseUser(u map[string]any, modes, items map[string]any) *User {
	if u == nil {
		return nil
	}
	user := newUser(u)
	if items == nil {
		items = mapOf(u["items"])
	}
	if modes == nil {
		modes = mapOf(u["modes"])
	}
	if items != nil {
		for k, v := range items {
			user.Items[k] = int64Of(v)
		}
	}
	if parsed := parseModes(modes); parsed != nil {
		user.Modes = parsed
		for level, fish := range parsed.Fish {
			user.Fish[level] = fish
		}
		user.Endless = parsed.Endless.Best
		user.SudokuBest = parsed.Sudoku.Best
		for level, stars := range parsed.Sudoku.Stars {
			user.SudokuStars[level] = stars
		}
		user.MatchBest = parsed.Match.Best
	}
	return user
}

// parseModes 通过 Modes 的自定义 Unmarshal 解析模式成绩。
func parseModes(modes map[string]any) *Modes {
	if modes == nil {
		return nil
	}
	raw, err := json.Marshal(modes)
	if err != nil {
		return nil
	}
	var m Modes
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil
	}
	return &m
}

// parseRecords 解析 records 数组。
func parseRecords(v any) []Record {
	items, _ := v.([]any)
	if len(items) == 0 {
		return nil
	}
	records := make([]Record, 0, len(items))
	for _, item := range items {
		m := mapOf(item)
		if m == nil {
			continue
		}
		records = append(records, Record{
			Level:     numOf(m["level"]),
			Stars:     numOf(m["stars"]),
			BestSteps: numOf(m["best_steps"]),
			Clears:    numOf(m["clears"]),
			Fails:     numOf(m["fails"]),
		})
	}
	return records
}

// mapOf 把 JSON 对象转成 map。
func mapOf(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

// numOf 把任意 JSON 数值转成 int。
func numOf(v any) int {
	switch x := v.(type) {
	case nil:
		return 0
	case json.Number:
		if i, err := x.Int64(); err == nil {
			return int(i)
		}
		if f, err := x.Float64(); err == nil {
			return int(f)
		}
		return 0
	case float64:
		return int(x)
	case float32:
		return int(x)
	case int:
		return x
	case int64:
		return int(x)
	case int32:
		return int(x)
	case uint64:
		return int(x)
	case bool:
		if x {
			return 1
		}
		return 0
	case string:
		s := strings.TrimSpace(x)
		if i, err := strconv.Atoi(s); err == nil {
			return i
		}
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return int(f)
		}
		return 0
	}
	return 0
}

// int64Of 把任意 JSON 数值转成 int64。
func int64Of(v any) int64 {
	switch x := v.(type) {
	case nil:
		return 0
	case json.Number:
		if i, err := x.Int64(); err == nil {
			return i
		}
		if f, err := x.Float64(); err == nil {
			return int64(f)
		}
		return 0
	case float64:
		return int64(x)
	case float32:
		return int64(x)
	case int:
		return int64(x)
	case int64:
		return x
	case int32:
		return int64(x)
	case uint64:
		return int64(x)
	case bool:
		if x {
			return 1
		}
		return 0
	case string:
		s := strings.TrimSpace(x)
		if i, err := strconv.ParseInt(s, 10, 64); err == nil {
			return i
		}
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return int64(f)
		}
		return 0
	}
	return 0
}

// strOf 把任意 JSON 值转成 string。
func strOf(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case json.Number:
		return x.String()
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case bool:
		if x {
			return "true"
		}
		return "false"
	}
	return fmt.Sprint(v)
}

// boolOf 把任意 JSON 值转成 bool。
func boolOf(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case json.Number:
		i, _ := x.Int64()
		return i != 0
	case float64:
		return x != 0
	case int:
		return x != 0
	case string:
		return x == "1" || strings.EqualFold(x, "true")
	}
	return false
}
