package sigproc

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/utrading/utrading-hl-monitor/pkg/goplus"
)

type HandlerFunc func(os.Signal)

func GracefulShutdown(shutdown HandlerFunc) {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)

	goplus.Go(func() {
		sig := <-sigChan
		log.Info().Msg(fmt.Sprintf("received signal: %s", sig.String()))

		goplus.Go(func() {
			shutdown(sig)
		})

		select {
		case <-time.After(30 * time.Second):
		}

		os.Exit(0)
	})
}
