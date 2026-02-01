-- 创建 HL 地址信号表
CREATE TABLE IF NOT EXISTS hl_address_signal (
    id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY COMMENT '主键ID',

    -- 地址信息
    address VARCHAR(42) NOT NULL COMMENT '监控地址',
    position_size VARCHAR(16) NOT NULL COMMENT '仓位大小: Small/Medium/Large',

    -- 交易信息
    symbol VARCHAR(24) NOT NULL COMMENT '交易对',
    coin_type VARCHAR(8) NOT NULL COMMENT '币种类型',
    asset_type VARCHAR(24) NOT NULL COMMENT '资产类型: spot/futures',
    direction VARCHAR(8) NOT NULL COMMENT '仓位方向 open/close',
    side VARCHAR(8) NOT NULL COMMENT '方向: LONG/SHORT',
    price DECIMAL(28,12) NOT NULL COMMENT '价格',
    size DECIMAL(18,8) NOT NULL COMMENT '数量',

    -- 订单关联
    oid BIGINT COMMENT '订单ID',

    -- 时间字段
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '创建时间',
    expired_at TIMESTAMP NOT NULL COMMENT '过期时间(7天后)',

    -- 索引
    INDEX idx_address (address),
    INDEX idx_symbol (symbol),
    INDEX idx_asset_type (asset_type),
    INDEX idx_created (created_at),
    INDEX idx_expired (expired_at),
    INDEX idx_oid (oid)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='HL地址信号表';
