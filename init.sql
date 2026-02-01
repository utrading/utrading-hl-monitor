-- uTrading HL Monitor 数据库初始化脚本
-- 创建时间: 2026-01-19
-- 说明: Hyperliquid 监控服务所需的数据库表结构

-- ============================================
-- 1. 监控地址配置表
-- ============================================
CREATE TABLE IF NOT EXISTS hl_watch_addresses (
    id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    player_id BIGINT UNSIGNED NOT NULL COMMENT '玩家ID',
    address VARCHAR(42) NOT NULL COMMENT '链上地址',
    nickname VARCHAR(64) DEFAULT '' COMMENT '自定义昵称',
    is_system TINYINT(1) NOT NULL DEFAULT 0 COMMENT '是否系统地址池',
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL DEFAULT NULL,
    UNIQUE KEY uidx_player_addr (player_id, address),
    KEY idx_player (player_id),
    KEY idx_deleted_at (deleted_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci
COMMENT='监控地址配置表';

-- ============================================
-- 2. 仓位缓存表
-- ============================================
CREATE TABLE IF NOT EXISTS hl_position_cache (
    id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    address VARCHAR(42) NOT NULL UNIQUE COMMENT '链上地址',
    spot_balances JSON COMMENT '现货余额JSON: [{coin, total, hold, entry_ntl}]',
    spot_total_usd VARCHAR(32) NOT NULL DEFAULT '0' COMMENT '现货总价值USD',
    futures_positions JSON COMMENT '合约仓位JSON: [{coin, szi, entry_px, unrealized_pnl, leverage, margin_used, position_value, return_on_equity}]',
    account_value VARCHAR(32) NOT NULL DEFAULT '0' COMMENT '账户总价值',
    total_margin_used VARCHAR(32) NOT NULL DEFAULT '0' COMMENT '总保证金使用',
    total_ntl_pos VARCHAR(32) NOT NULL DEFAULT '0' COMMENT '总净仓位',
    withdrawable VARCHAR(32) NOT NULL DEFAULT '0' COMMENT '可提取金额',
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    INDEX idx_updated (updated_at),
    INDEX uidx_address (address)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci
COMMENT='Hyperliquid仓位缓存表';

-- ============================================
-- 3. 订单聚合表
-- ============================================
CREATE TABLE IF NOT EXISTS hl_order_aggregation (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    oid BIGINT NOT NULL COMMENT 'Hyperliquid订单ID',
    address VARCHAR(42) NOT NULL COMMENT '监控地址',
    symbol VARCHAR(24) NOT NULL COMMENT '交易对 (BTCUSDC)',
    direction VARCHAR(16) NOT NULL COMMENT '订单方向: Open Long/Close Long等',
    fills JSON NOT NULL COMMENT '成交记录JSON',
    total_size DECIMAL(18,8) NOT NULL DEFAULT 0 COMMENT '总成交数量',
    weighted_avg_px DECIMAL(28,12) NOT NULL DEFAULT 0 COMMENT '加权平均价格',
    order_status VARCHAR(16) NOT NULL DEFAULT 'open' COMMENT '订单状态: open/filled/canceled',
    last_fill_time BIGINT NOT NULL COMMENT '最后成交时间(毫秒时间戳)',
    signal_sent BOOLEAN NOT NULL DEFAULT FALSE COMMENT '信号是否已发送',
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    INDEX idx_oid (oid),
    INDEX idx_address (address),
    INDEX idx_direction (direction),
    INDEX idx_last_fill_time (last_fill_time),
    INDEX idx_updated_at (updated_at),
    INDEX idx_signal_sent (signal_sent)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci
COMMENT='订单聚合状态表';

-- ============================================
-- 4. 地址信号表
-- ============================================
CREATE TABLE IF NOT EXISTS hl_address_signals (
    id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    address VARCHAR(42) NOT NULL COMMENT '监控地址',
    position_rate VARCHAR(16) NOT NULL COMMENT '仓位比例: 百分比字符串 (15.50%)',
    symbol VARCHAR(24) NOT NULL COMMENT '交易对',
    coin_type VARCHAR(8) NOT NULL COMMENT '币种类型',
    asset_type VARCHAR(24) NOT NULL COMMENT '资产类型: spot/futures',
    direction VARCHAR(8) NOT NULL COMMENT '仓位方向: open/close',
    side VARCHAR(8) NOT NULL COMMENT '方向: LONG/SHORT',
    price DECIMAL(28,12) NOT NULL COMMENT '价格',
    size DECIMAL(18,8) NOT NULL COMMENT '数量',
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expired_at TIMESTAMP NOT NULL COMMENT '过期时间(7天后)',
    INDEX idx_address (address),
    INDEX idx_symbol (symbol),
    INDEX idx_asset_type (asset_type),
    INDEX idx_created (created_at),
    INDEX idx_expired (expired_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci
COMMENT='HL地址交易信号表';

-- ============================================
-- 5. 插入测试数据
-- ============================================
INSERT INTO hl_watch_addresses (player_id, address, nickname, is_system) VALUES
    (1, '0x87f9cd15f5050a9283b8896300f7c8cf69ece2cf', 'Trader 1', 1),
    (1, '0x162cc7c861ebd0c06b3d72319201150482518185', 'Trader 2', 1),
    (1, '0x399965e15d4e61ec3529cc98b7f7ebb93b733336', 'Trader 3', 1),
    (1, '0x50b309f78e774a756a2230e1769729094cac9f20', 'Trader 4', 1),
    (1, '0xff4cd3826ecee12acd4329aada4a2d3419fc463c', 'Trader 5', 1),
    (1, '0x7b7f72a28fe109fa703eeed7984f2a8a68fedee2', 'Trader 6', 1),
    (1, '0x7839e2f2c375dd2935193f2736167514efff9916', 'Trader 7', 1)
ON DUPLICATE KEY UPDATE updated_at = CURRENT_TIMESTAMP;
