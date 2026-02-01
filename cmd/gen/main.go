package main

import (
	"github.com/utrading/utrading-hl-monitor/internal/dal"
)

func main() {
	dal.GenExecute("./internal/dal/gen", dal.MySQL())
}
