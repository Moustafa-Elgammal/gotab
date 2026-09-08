// Scratch instrument for V6.5 / Phase 7 (D46): enumerate, then call darwin.Raise on every window and
// report the result per origin. It made the finding concrete — `cg`-only windows return ErrNoWindow
// while AX-visible ones raise. Kept through Phase 7; delete when V6.13 closes.
package main

import (
	"fmt"

	"github.com/Moustafa-Elgammal/gotab/internal/core"
	"github.com/Moustafa-Elgammal/gotab/internal/platform/darwin"
)

func main() {
	if err := darwin.Init(); err != nil {
		fmt.Println("init:", err)
		return
	}
	e := darwin.NewEnumerator()
	ws, err := e.Enumerate(make([]core.Window, 0, 64))
	if err != nil {
		fmt.Println("enumerate:", err)
		return
	}
	origins := e.Origins()
	fmt.Printf("%d switchable windows\n\n", len(ws))
	for i, w := range ws {
		o := "?"
		if i < len(origins) {
			o = origins[i].String()
		}
		onscreen := "-"
		if w.Flags&core.FlagOnScreen != 0 {
			onscreen = "o"
		}
		rerr := darwin.Raise(w.ID)
		res := "OK — raised"
		if rerr != nil {
			res = rerr.Error()
		}
		fmt.Printf("  id=%-6d origin=%-4s screen=%s  %-18s | %s\n", w.ID, o, onscreen, w.AppName, res)
	}
}
