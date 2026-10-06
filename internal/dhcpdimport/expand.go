// Copyright 2026 Кислов Роман Сергеевич
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package dhcpdimport

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const maxIncludeDepth = 32

// Expand reads path and recursively inlines ISC `include "file";` directives.
// Relative includes are resolved against the directory of the including file.
func Expand(path string) (string, []string, error) {
	var files []string
	text, err := expandFile(path, map[string]bool{}, map[string]bool{}, 0, &files)
	if err != nil {
		return "", files, err
	}
	return text, files, nil
}

func expandFile(path string, stack, done map[string]bool, depth int, files *[]string) (string, error) {
	if depth > maxIncludeDepth {
		return "", fmt.Errorf("include nesting deeper than %d at %s", maxIncludeDepth, path)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if stack[abs] {
		return "", fmt.Errorf("include cycle involving %s", abs)
	}
	if done[abs] {
		// Already inlined once; skip duplicate include of the same file.
		return fmt.Sprintf("\n# skipped duplicate include %s\n", abs), nil
	}
	stack[abs] = true
	defer delete(stack, abs)
	raw, err := os.ReadFile(abs)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", abs, err)
	}
	*files = append(*files, abs)
	text, err := expandSource(string(raw), filepath.Dir(abs), stack, done, depth, files)
	if err != nil {
		return "", err
	}
	done[abs] = true
	return text, nil
}

func expandSource(src, baseDir string, stack, done map[string]bool, depth int, files *[]string) (string, error) {
	var out strings.Builder
	i := 0
	for i < len(src) {
		if src[i] == '#' {
			end := strings.IndexByte(src[i:], '\n')
			if end < 0 {
				out.WriteString(src[i:])
				break
			}
			out.WriteString(src[i : i+end+1])
			i += end + 1
			continue
		}
		if src[i] == '"' || src[i] == '\'' {
			q := src[i]
			out.WriteByte(q)
			i++
			for i < len(src) {
				out.WriteByte(src[i])
				if src[i] == '\\' && i+1 < len(src) {
					i++
					out.WriteByte(src[i])
					i++
					continue
				}
				if src[i] == q {
					i++
					break
				}
				i++
			}
			continue
		}
		if matchKeyword(src, i, "include") {
			j := i + len("include")
			for j < len(src) && isSpace(src[j]) {
				j++
			}
			if j >= len(src) || (src[j] != '"' && src[j] != '\'') {
				out.WriteByte(src[i])
				i++
				continue
			}
			q := src[j]
			j++
			start := j
			for j < len(src) && src[j] != q {
				j++
			}
			if j >= len(src) {
				return "", fmt.Errorf("unterminated include path near %q", snippet(src, i))
			}
			incPath := src[start:j]
			j++
			for j < len(src) && isSpace(src[j]) {
				j++
			}
			if j < len(src) && src[j] == ';' {
				j++
			}
			resolved := incPath
			if !filepath.IsAbs(resolved) {
				resolved = filepath.Join(baseDir, resolved)
			}
			nested, err := expandFile(resolved, stack, done, depth+1, files)
			if err != nil {
				return "", err
			}
			out.WriteString("\n# begin include ")
			out.WriteString(resolved)
			out.WriteString("\n")
			out.WriteString(nested)
			if !strings.HasSuffix(nested, "\n") {
				out.WriteByte('\n')
			}
			out.WriteString("# end include ")
			out.WriteString(resolved)
			out.WriteString("\n")
			i = j
			continue
		}
		out.WriteByte(src[i])
		i++
	}
	return out.String(), nil
}

func matchKeyword(src string, i int, kw string) bool {
	if i+len(kw) > len(src) {
		return false
	}
	if !strings.EqualFold(src[i:i+len(kw)], kw) {
		return false
	}
	if i > 0 && isIdent(src[i-1]) {
		return false
	}
	end := i + len(kw)
	if end < len(src) && isIdent(src[end]) {
		return false
	}
	return true
}

func isIdent(b byte) bool {
	return b == '_' || b == '-' || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

func isSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r'
}

func snippet(src string, i int) string {
	end := i + 40
	if end > len(src) {
		end = len(src)
	}
	return strings.ReplaceAll(src[i:end], "\n", "\\n")
}
