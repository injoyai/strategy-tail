package script

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestCheckScriptDoesNotExecuteInitializers(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "executed")
	src := fmt.Sprintf(`package main
import "os"
var initialized = func() int { os.WriteFile(%q, []byte("global"), 0600); return 1 }()
func init() { os.WriteFile(%q, []byte("init"), 0600) }
`, marker, marker)
	if err := CheckScript(src); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("校验执行了初始化代码: %v", err)
	}
}
