-- 订单聚合状态表
CREATE TABLE IF NOT EXISTS hl_order_aggregation (
    oid BIGINT PRIMARY KEY COMMENT '订单ID',
    address VARCHAR(42) NOT NULL COMMENT '监控地址',
    symbol VARCHAR(24) NOT NULL COMMENT '交易对',

    -- 聚合数据
    fills JSON NOT NULL COMMENT '所有 fill 数据',
    total_size DECIMAL(18,8) NOT NULL DEFAULT 0 COMMENT '总数量',
    weighted_avg_px DECIMAL(28,12) NOT NULL DEFAULT 0 COMMENT '加权平均价',

    -- 状态控制
    order_status VARCHAR(16) NOT NULL DEFAULT 'open' COMMENT '订单状态: open/filled/canceled',
    last_fill_time BIGINT NOT NULL COMMENT '最后 fill 时间戳',

    -- 处理标记
    signal_sent BOOLEAN NOT NULL DEFAULT FALSE COMMENT '信号是否已发送',

    -- 时间字段
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,

    INDEX idx_address (address),
    INDEX idx_last_fill_time (last_fill_time),
    INDEX idx_signal_sent (signal_sent),
    INDEX idx_updated_at (updated_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='订单聚合状态表';
