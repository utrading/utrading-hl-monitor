package dao

import (
	"github.com/utrading/utrading-hl-monitor/internal/dal/gen"
	"github.com/utrading/utrading-hl-monitor/internal/models"
)

type PairConfigDAO struct{}

var _pairConfig = &PairConfigDAO{}

func PairConfig() *PairConfigDAO {
	return _pairConfig
}

func (d *PairConfigDAO) ListAll() ([]*models.PairConfig, error) {
	return gen.PairConfig.Find()
}
