package zoekt

import "log"

// Limits the number of matches on the SearchResult by the given positive limit,
// returning the remaining limit, if any.
func (result *SearchResult) LimitMatches(limit int, chunkMatches bool) int {
	var limiter func(file *FileMatch, limit int) int
	if chunkMatches {
		limiter = limitChunkMatches
	} else {
		limiter = limitLineMatches
	}
	for i := range result.Files {
		limit = limiter(&result.Files[i], limit)
		if limit == 0 {
			if i != len(result.Files)-1 {
				result.Files = result.Files[:i+1]
			}
			return 0
		}
	}
	return limit
}

// Limit the number of ChunkMatches in the given FileMatch, returning the
// remaining limit, if any.
func limitChunkMatches(file *FileMatch, limit int) int {
	for i := range file.ChunkMatches {
		cm := &file.ChunkMatches[i]
		if len(cm.Ranges) > limit {
			// We potentially need to effect the limit upon 3 different fields:
			// Ranges, SymbolInfo, and Content.

			// Content is the most complicated: we need to remove the last N
			// lines from it, where N is the difference between the line number
			// of the end of the old last Range and that of the new last Range.
			// This calculation is correct in the presence of both context lines
			// and multiline Ranges, taking into account that Content never has
			// a trailing newline.
			n := cm.Ranges[len(cm.Ranges)-1].End.LineNumber - cm.Ranges[limit-1].End.LineNumber
			if n > 0 {
				for b := len(cm.Content) - 1; b >= 0; b-- {
					if cm.Content[b] == '\n' {
						n -= 1
					}
					if n == 0 {
						cm.Content = cm.Content[:b]
						break
					}
				}
				if n > 0 {
					// Should be impossible.
					log.Panicf("Failed to find enough newlines when truncating Content, %d left over, %d ranges", n, len(cm.Ranges))
				}
			}

			cm.Ranges = cm.Ranges[:limit]
			if cm.SymbolInfo != nil {
				// When non-nil, SymbolInfo is specified to have the same length
				// as Ranges.
				cm.SymbolInfo = cm.SymbolInfo[:limit]
			}
		}
		if len(cm.Ranges) == limit {
			file.ChunkMatches = file.ChunkMatches[:i+1]
			limit = 0
			break
		}
		limit -= len(cm.Ranges)
	}
	return limit
}

// Limit the number of LineMatches in the given FileMatch, returning the
// remaining limit, if any.
func limitLineMatches(file *FileMatch, limit int) int {
	for i := range file.LineMatches {
		lm := &file.LineMatches[i]
		if len(lm.LineFragments) > limit {
			lm.LineFragments = lm.LineFragments[:limit]
		}
		if len(lm.LineFragments) == limit {
			file.LineMatches = file.LineMatches[:i+1]
			limit = 0
			break
		}
		limit -= len(lm.LineFragments)
	}
	return limit
}
