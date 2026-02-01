package goplus

import (
	"fmt"
	"runtime"
	"strings"

	"github.com/utrading/utrading-hl-monitor/pkg/logger"
)

func Recover() {
	if r := recover(); r != nil {
		const maxDepth = 32
		callers := make([]string, 0, maxDepth)
		for i := 1; i <= maxDepth; i++ {
			_, file, line, ok := runtime.Caller(i)
			if !ok {
				break
			}
			callers = append(callers, fmt.Sprintf("%s:%d", file, line))
		}

		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("panic: %v\ncallers:\n", r))
		for _, c := range callers {
			sb.WriteString(c)
			sb.WriteByte('\n')
		}

		logger.Error().Msg(sb.String())
	}
}
