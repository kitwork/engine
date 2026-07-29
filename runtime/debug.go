package runtime

import (
	"fmt"
	"sort"
)

// SourceLocation identifies the original source location for a bytecode offset.
type SourceLocation struct {
	File   string
	Line   int32
	Column int32
}

// DebugEntry starts a source-location range at IP. The range ends at the next
// entry, so compiler output stores transitions rather than one item per byte.
type DebugEntry struct {
	IP int
	SourceLocation
}

type debugEntry struct {
	ip     int
	fileID uint32
	line   int32
	column int32
}

type debugTable struct {
	files   []string
	entries []debugEntry
}

func newDebugTable(codeLength int, entries []DebugEntry) (debugTable, error) {
	if len(entries) == 0 {
		return debugTable{}, nil
	}

	table := debugTable{
		files:   make([]string, 0, 4),
		entries: make([]debugEntry, 0, len(entries)),
	}
	fileIDs := make(map[string]uint32)
	lastIP := -1
	var last SourceLocation

	for index, entry := range entries {
		if entry.IP < 0 || entry.IP >= codeLength {
			return debugTable{}, fmt.Errorf(
				"debug entry %d has byte offset %d outside program length %d",
				index,
				entry.IP,
				codeLength,
			)
		}
		if entry.IP <= lastIP {
			return debugTable{}, fmt.Errorf(
				"debug entry %d has non-increasing byte offset %d",
				index,
				entry.IP,
			)
		}
		if entry.Line < 0 || entry.Column < 0 {
			return debugTable{}, fmt.Errorf(
				"debug entry %d has invalid source location %d:%d",
				index,
				entry.Line,
				entry.Column,
			)
		}

		location := entry.SourceLocation
		if len(table.entries) > 0 && location == last {
			lastIP = entry.IP
			continue
		}

		fileID, ok := fileIDs[location.File]
		if !ok {
			fileID = uint32(len(table.files))
			fileIDs[location.File] = fileID
			table.files = append(table.files, location.File)
		}
		table.entries = append(table.entries, debugEntry{
			ip:     entry.IP,
			fileID: fileID,
			line:   location.Line,
			column: location.Column,
		})
		lastIP = entry.IP
		last = location
	}
	return table, nil
}

func debugEntriesFromSourceMap(sourceMap []int32) []DebugEntry {
	entries := make([]DebugEntry, 0)
	var previous int32 = -1
	for ip, line := range sourceMap {
		if ip == 0 || line != previous {
			entries = append(entries, DebugEntry{
				IP: ip,
				SourceLocation: SourceLocation{
					Line: line,
				},
			})
			previous = line
		}
	}
	return entries
}

func (table *debugTable) location(ip int) SourceLocation {
	if table == nil || ip < 0 || len(table.entries) == 0 {
		return SourceLocation{}
	}
	index := sort.Search(len(table.entries), func(index int) bool {
		return table.entries[index].ip > ip
	}) - 1
	if index < 0 {
		return SourceLocation{}
	}

	entry := table.entries[index]
	location := SourceLocation{
		Line:   entry.line,
		Column: entry.column,
	}
	if int(entry.fileID) < len(table.files) {
		location.File = table.files[entry.fileID]
	}
	return location
}

func (table *debugTable) snapshot() []DebugEntry {
	if table == nil || len(table.entries) == 0 {
		return nil
	}
	entries := make([]DebugEntry, len(table.entries))
	for index, entry := range table.entries {
		entries[index] = DebugEntry{
			IP: entry.ip,
			SourceLocation: SourceLocation{
				Line:   entry.line,
				Column: entry.column,
			},
		}
		if int(entry.fileID) < len(table.files) {
			entries[index].File = table.files[entry.fileID]
		}
	}
	return entries
}
