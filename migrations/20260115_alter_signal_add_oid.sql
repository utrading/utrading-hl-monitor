-- 为信号表添加 oid 字段（用于关联订单）
-- 检查列是否存在，不存在则添加
SET @column_exists = (
    SELECT COUNT(*)
    FROM INFORMATION_SCHEMA.COLUMNS
    WHERE TABLE_SCHEMA = 'utrading'
    AND TABLE_NAME = 'hl_address_signal'
    AND COLUMN_NAME = 'oid'
);

SET @sql = IF(@column_exists = 0,
    'ALTER TABLE hl_address_signal ADD COLUMN oid BIGINT COMMENT ''订单ID''',
    'SELECT ''Column oid already exists'' AS message'
);

PREPARE stmt FROM @sql;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

-- 添加索引（如果不存在）
SET @index_exists = (
    SELECT COUNT(*)
    FROM INFORMATION_SCHEMA.STATISTICS
    WHERE TABLE_SCHEMA = 'utrading'
    AND TABLE_NAME = 'hl_address_signal'
    AND INDEX_NAME = 'idx_oid'
);

SET @sql = IF(@index_exists = 0,
    'ALTER TABLE hl_address_signal ADD INDEX idx_oid (oid)',
    'SELECT ''Index idx_oid already exists'' AS message'
);

PREPARE stmt FROM @sql;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
