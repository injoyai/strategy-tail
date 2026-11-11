-- Load this fixture into an empty test database before using its SELECT-only account.
SET NAMES utf8mb4;
SET time_zone='+00:00';
CREATE TABLE cn_stock_kline_day (code VARCHAR(32), time DATE, open DOUBLE, high DOUBLE, low DOUBLE, close DOUBLE, volume BIGINT, amount DOUBLE, PRIMARY KEY(code,time));
CREATE TABLE cn_index_kline_day LIKE cn_stock_kline_day;
INSERT INTO cn_stock_kline_day VALUES ('sh600000','2026-11-10',11,12,11,12,100,1200);
INSERT INTO cn_index_kline_day VALUES ('sh000001','2026-11-10',3000,3001,2999,3000,101,1200);
CREATE TABLE cn_stock_kline_minute_2026 (code VARCHAR(32), time DATETIME, open DOUBLE, high DOUBLE, low DOUBLE, close DOUBLE, volume BIGINT, amount DOUBLE, source VARCHAR(8) DEFAULT '', PRIMARY KEY(code,time));
INSERT INTO cn_stock_kline_minute_2026 VALUES
  ('sh600000','2026-11-09 15:00:00',9,99,1,9,100,900,''),
  ('sh600000','2026-11-10 09:31:00',10,12,9,10.5,100,1000,''),
  ('sh600000','2026-11-10 09:32:00',10.5,11,10,11,200,NULL,''),
  ('sh600000','2026-11-10 09:33:00',11,100,1,20,100,2000,'');
CREATE TABLE codes (exchange VARCHAR(8), code VARCHAR(32), name VARCHAR(64), multiple INT, `decimal` INT, last_price DOUBLE, PRIMARY KEY(exchange,code));
INSERT INTO codes VALUES ('sh','600000','浦发银行',100,2,12);
CREATE TABLE gbbq (id BIGINT AUTO_INCREMENT PRIMARY KEY, code VARCHAR(32), time DATETIME, category INT, c1 DOUBLE, c2 DOUBLE, c3 DOUBLE, c4 DOUBLE);
INSERT INTO gbbq (code,time,category,c1,c2,c3,c4) VALUES ('sh600000','2026-11-10 00:00:00',5,0,0,1000,2000),('sh600000','2026-11-11 00:00:00',5,0,0,9999,9999);
CREATE TABLE research_records (dataset VARCHAR(64), code VARCHAR(32), record_key VARCHAR(128), source VARCHAR(64), revision VARCHAR(64), event_at BIGINT, available_at BIGINT, values_json JSON, attributes_json JSON);
INSERT INTO research_records VALUES ('valuation.daily','sh600000','2026-11-10','fixture','1',UNIX_TIMESTAMP('2026-11-10 07:00:00')*1000000000,UNIX_TIMESTAMP('2026-11-10 07:00:00')*1000000000,JSON_OBJECT('pe_ttm',12),JSON_OBJECT());
INSERT INTO research_records VALUES ('valuation.daily','sh600000','2026-11-10','fixture','2',UNIX_TIMESTAMP('2026-11-10 07:00:00')*1000000000,UNIX_TIMESTAMP('2026-11-11 00:00:00')*1000000000,JSON_OBJECT('pe_ttm',13),JSON_OBJECT());
CREATE TABLE universe_members (code VARCHAR(32) PRIMARY KEY, listed_at DATE, delisted_at DATE, available_at BIGINT);
INSERT INTO universe_members VALUES ('sh600000','1999-11-10',NULL,UNIX_TIMESTAMP('1999-11-10 00:00:00')*1000000000);
