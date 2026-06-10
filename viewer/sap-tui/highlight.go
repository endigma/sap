package main

import (
	"bytes"
	"strings"
	"sync"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
)

var (
	highlightCacheMu sync.Mutex
	highlightCache   = make(map[string]string)
)

func highlightCode(lang, src string) string {
	cacheKey := lang + "\x00" + src
	highlightCacheMu.Lock()
	if highlighted, ok := highlightCache[cacheKey]; ok {
		highlightCacheMu.Unlock()
		return highlighted
	}
	highlightCacheMu.Unlock()

	lexer := lexers.Get(lang)
	if lexer == nil {
		lexer = lexers.Fallback
	}
	lexer = chroma.Coalesce(lexer)
	style := styles.Get("monokai")
	if style == nil {
		style = styles.Fallback
	}
	formatter := formatters.Get("terminal256")
	if formatter == nil {
		formatter = formatters.Fallback
	}
	iterator, err := lexer.Tokenise(nil, src)
	if err != nil {
		return src
	}
	var buf bytes.Buffer
	if err := formatter.Format(&buf, style, iterator); err != nil {
		return src
	}
	highlighted := strings.TrimRight(buf.String(), "\n")
	highlightCacheMu.Lock()
	highlightCache[cacheKey] = highlighted
	highlightCacheMu.Unlock()
	return highlighted
}

func codeLanguage(hints []string) string {
	for _, hint := range hints {
		if strings.HasPrefix(hint, "code:") {
			return strings.TrimPrefix(hint, "code:")
		}
	}
	for _, hint := range hints {
		if hint == "code" {
			return ""
		}
	}
	return ""
}
