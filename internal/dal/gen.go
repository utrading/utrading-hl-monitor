package dal

import (
	"gorm.io/gen"
	"gorm.io/gorm"

	"github.com/utrading/utrading-hl-monitor/internal/models"
)

// GenExecute 生成 gorm-gen 代码
// 命令使用: go run cmd/gen/main.go
func GenExecute(outPath string, db *gorm.DB) {
	g := gen.NewGenerator(gen.Config{
		OutPath: outPath,
		Mode:    gen.WithoutContext | gen.WithDefaultQuery | gen.WithQueryInterface,
	})

	// 设置数据库
	g.UseDB(db)

	// 应用模型生成查询接口
	g.ApplyBasic(
		models.HlWatchAddress{},
		models.HlPositionCache{},
		models.OrderAggregation{},
		models.HlAddressSignal{},
		models.HlActiveAddress{},
	)

	g.Execute()
}
