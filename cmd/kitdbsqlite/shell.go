package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/kitwork/engine/internal/benchclock"
	kitdb "github.com/kitwork/engine/kitdb/sqlite"
)

const shellHelp = `Gõ một câu SQL, kết thúc bằng dấu ;  (đọc, ghi hay đổi schema đều được; mỗi lần một câu)
  .search <bảng> <chữ>     tìm toàn văn, mọi từ phải có     (.any: một từ là đủ, .prefix: tiền tố, .phrase: cụm từ)
  .tables                  các bảng và số dòng
  .history [n]             n commit gần nhất (mặc định 10)
  .verify                  kiểm chuỗi checksum của lịch sử
  .anchor                  chép một mốc để khôi phục
  .restore <rev> <file>    khôi phục database như lúc rev đó vào một file mới
  .revision                rev hiện tại
  .help  .quit`

func (t *tool) shell(ctx context.Context) {
	fmt.Println(shellHelp)
	if len(t.search) > 0 {
		for _, table := range t.search {
			fmt.Printf("bảng tìm được: %s (lần tìm đầu dựng chỉ mục, với bảng lớn mất vài phút)\n", table.Table)
		}
	}
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 1<<20), 16<<20)
	var pending strings.Builder
	prompt := func() {
		if pending.Len() == 0 {
			fmt.Print("kitdb> ")
		} else {
			fmt.Print("  ...> ")
		}
	}
	prompt()
	for scanner.Scan() {
		line := scanner.Text()
		if pending.Len() == 0 && strings.HasPrefix(strings.TrimSpace(line), ".") {
			if err := t.command(ctx, strings.TrimSpace(line)); err == errQuit {
				return
			} else if err != nil {
				fmt.Println("lỗi:", err)
			}
			prompt()
			continue
		}
		pending.WriteString(line)
		pending.WriteString("\n")
		if !strings.HasSuffix(strings.TrimSpace(line), ";") {
			prompt()
			continue
		}
		statement := strings.TrimSuffix(strings.TrimSpace(pending.String()), ";")
		pending.Reset()
		result, err := t.run(ctx, statement)
		if err != nil {
			fmt.Println("lỗi:", err)
		} else {
			printResult(result)
		}
		prompt()
	}
}

var spaces = regexp.MustCompile(`\s+`)

func (t *tool) command(ctx context.Context, line string) error {
	parts := spaces.Split(line, 3)
	switch parts[0] {
	case ".quit", ".exit":
		return errQuit
	case ".help":
		fmt.Println(shellHelp)
	case ".revision":
		fmt.Println("rev", t.database.Revision())
	case ".tables":
		result, err := t.run(ctx, `SELECT name FROM sqlite_schema WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
		if err != nil {
			return err
		}
		for _, row := range result.Rows {
			name := row[0].(string)
			count, err := t.run(ctx, `SELECT count(*) FROM `+quote(name))
			if err != nil {
				return err
			}
			fmt.Printf("  %-24s %v dòng (%.1f ms)\n", name, count.Rows[0][0], count.Milliseconds)
		}
	case ".search", ".any", ".prefix", ".phrase":
		if len(parts) < 3 {
			return fmt.Errorf("viết %s <bảng> <chữ>", parts[0])
		}
		mode := map[string]string{".search": "all", ".any": "any", ".prefix": "prefix", ".phrase": "phrase"}[parts[0]]
		fmt.Println("đang tìm…")
		result, err := t.find(ctx, parts[1], parts[2], mode, 20)
		if err != nil {
			return err
		}
		fmt.Printf("%d kết quả đầu, rev %d, %.2f ms (cả đọc dòng)\n", len(result.Hits), result.Revision, result.Milliseconds)
		for position, hit := range result.Hits {
			var parts []string
			for _, field := range hit.Highlighted[1:] {
				if field.HTML != "" {
					parts = append(parts, fmt.Sprintf("%s=%s", field.Column, plain(field.HTML)))
				}
			}
			for _, column := range t.show[result.Table] {
				parts = append(parts, fmt.Sprintf("%s=%v", column, hit.Values[column]))
			}
			first := plain(hit.Highlighted[0].HTML)
			fmt.Printf("%2d. [%d] %.3f  %s  %s\n", position+1, hit.RowID, hit.Score, first, strings.Join(parts, " "))
		}
	case ".history":
		count := 10
		if len(parts) > 1 {
			count, _ = strconv.Atoi(parts[1])
		}
		var commits []kitdb.Commit
		after := uint64(0)
		if revision := t.database.Revision(); revision > uint64(count) {
			after = revision - uint64(count)
		}
		if err := t.database.Changes(ctx, after, func(commit kitdb.Commit) error {
			commits = append(commits, commit)
			return nil
		}); err != nil {
			return err
		}
		for _, commit := range commits {
			fmt.Printf("  rev %d  %s  %d thay đổi", commit.Revision, commit.At.Local().Format("2006-01-02 15:04:05.000"), len(commit.Changes))
			for position, change := range commit.Changes {
				if position == 3 {
					fmt.Print(" …")
					break
				}
				fmt.Printf("  %s %s #%d", operationName(change.Operation), change.Table, change.RowID)
				if change.Operation == kitdb.Statement {
					fmt.Printf(" %q", change.Statement)
				}
			}
			fmt.Println()
		}
	case ".verify":
		start := benchclock.Now()
		last, err := t.database.VerifyHistory(ctx)
		if err != nil {
			return err
		}
		fmt.Printf("lịch sử liền mạch tới rev %d (%s)\n", last, since(start))
	case ".anchor":
		start := benchclock.Now()
		revision, err := t.database.Anchor(ctx)
		if err != nil {
			return err
		}
		fmt.Printf("mốc ở rev %d (%s)\n", revision, since(start))
	case ".restore":
		if len(parts) < 3 {
			return fmt.Errorf("viết .restore <rev> <file mới>")
		}
		revision, err := strconv.ParseUint(parts[1], 10, 64)
		if err != nil {
			return err
		}
		var moment time.Time
		if err := t.database.Changes(ctx, revision-1, func(commit kitdb.Commit) error {
			if commit.Revision == revision {
				moment = commit.At
			}
			return nil
		}); err != nil {
			return err
		}
		if moment.IsZero() {
			return fmt.Errorf("không có rev %d trong lịch sử", revision)
		}
		start := benchclock.Now()
		restored, err := t.database.Restore(ctx, moment, parts[2])
		if err != nil {
			return err
		}
		fmt.Printf("đã khôi phục tới rev %d vào %s (%s)\n", restored, parts[2], since(start))
	default:
		return fmt.Errorf("không biết lệnh %s; gõ .help", parts[0])
	}
	return nil
}

func plain(html string) string {
	html = strings.NewReplacer("<mark>", "\x1b[1;33m", "</mark>", "\x1b[0m").Replace(html)
	return strings.NewReplacer("&amp;", "&", "&lt;", "<", "&gt;", ">", "&quot;", `"`, "&#39;", "'").Replace(html)
}

func operationName(operation kitdb.Operation) string {
	return map[kitdb.Operation]string{kitdb.Insert: "thêm", kitdb.Update: "sửa", kitdb.Delete: "xoá", kitdb.Statement: "schema"}[operation]
}

func printResult(result queryResult) {
	switch result.Kind {
	case "write", "schema":
		fmt.Printf("đã ghi: rev %d, %d dòng, %.2f ms\n", result.Revision, result.RowsAffected, result.Milliseconds)
		return
	}
	widths := make([]int, len(result.Columns))
	cells := make([][]string, len(result.Rows))
	for position, column := range result.Columns {
		widths[position] = min(len([]rune(column)), 40)
	}
	for row, values := range result.Rows {
		cells[row] = make([]string, len(values))
		for position, value := range values {
			text := strings.ReplaceAll(fmt.Sprint(value), "\n", " ")
			if value == nil {
				text = "NULL"
			}
			if runes := []rune(text); len(runes) > 40 {
				text = string(runes[:39]) + "…"
			}
			cells[row][position] = text
			widths[position] = max(widths[position], len([]rune(text)))
		}
	}
	line := func(values []string) {
		for position, value := range values {
			fmt.Printf("%s%s  ", value, strings.Repeat(" ", widths[position]-len([]rune(value))))
		}
		fmt.Println()
	}
	line(result.Columns)
	for _, row := range cells {
		line(row)
	}
	more := ""
	if result.Truncated {
		more = fmt.Sprintf(" (chỉ in %d dòng đầu)", maximumRows)
	}
	fmt.Printf("%d dòng%s, rev %d, %.2f ms\n", len(result.Rows), more, result.Revision, result.Milliseconds)
}
