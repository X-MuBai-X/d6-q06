package httpapi

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// testPassword 是测试专用的账号口令。
// 按 AGENTS.md §5 第 4 条，演示口令的完整值只保留在 cmd/server/main.go，
// 测试代码与脚本一律使用与演示口令不同的测试专用口令。
const testPassword = "TestOnly@2026"

// demoPasswordPattern 匹配 cmd/server/main.go 中的演示口令常量。
var demoPasswordPattern = regexp.MustCompile(`demoPassword\s*=\s*"([^"]+)"`)

// demoPasswordFromSource 从 cmd/server/main.go 读取演示口令的真实值。
// 测试代码不硬编码该值，避免明文再次扩散。
func demoPasswordFromSource(t *testing.T) string {
	t.Helper()
	path := filepath.Join("..", "..", "cmd", "server", "main.go")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取演示口令来源失败: %v", err)
	}
	m := demoPasswordPattern.FindSubmatch(raw)
	if m == nil {
		t.Fatalf("在 %s 中没有找到演示口令常量", path)
	}
	return string(m[1])
}

// TestTestPasswordDiffersFromDemoPassword 证明测试口令与演示口令不是同一个值。
func TestTestPasswordDiffersFromDemoPassword(t *testing.T) {
	demo := demoPasswordFromSource(t)
	if demo == "" {
		t.Fatal("演示口令不应为空")
	}
	if testPassword == "" {
		t.Fatal("测试口令不应为空")
	}
	if testPassword == demo {
		t.Fatal("测试口令必须与演示口令不同")
	}
}

// TestNoPlaintextDemoPasswordInPackageFiles 证明本包（含测试）不再出现演示口令明文。
func TestNoPlaintextDemoPasswordInPackageFiles(t *testing.T) {
	demo := demoPasswordFromSource(t)
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("读取包目录失败: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		raw, err := os.ReadFile(e.Name())
		if err != nil {
			t.Fatalf("读取 %s 失败: %v", e.Name(), err)
		}
		if strings.Contains(string(raw), demo) {
			t.Fatalf("%s 仍然包含演示口令明文", e.Name())
		}
	}
}
