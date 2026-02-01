-- 添加 direction 字段到 hl_order_aggregation 表
-- 用于支持反手订单拆分（同一 Oid 可能有多条记录，不同 direction）

ALTER TABLE hl_order_aggregation
ADD COLUMN direction VARCHAR(20) NOT NULL DEFAULT '' AFTER symbol;

-- 添加索引以优化查询
ALTER TABLE hl_order_aggregation
ADD INDEX idx_direction (direction);

-- 更新主键（oid 单独作为主键可能冲突）
-- 移除旧的主键
ALTER TABLE hl_order_aggregation
DROP PRIMARY KEY;

-- 添加自增 ID 作为新的主键
ALTER TABLE hl_order_aggregation
ADD COLUMN id BIGINT AUTO_INCREMENT PRIMARY KEY FIRST;

-- oid 改为普通索引（如果需要按 oid 查询）
ALTER TABLE hl_order_aggregation
ADD INDEX idx_oid (oid);
