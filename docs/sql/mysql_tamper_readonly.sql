-- NeuralGate 审计库权限隔离模板（MySQL）
-- 目的：PRD 3.5「审计库独立隔离」——业务账号对审计相关表仅有 SELECT 权限，
--       无 UPDATE/DELETE，确保审计日志不可篡改、不可删除。
-- 用法：由 DBA 在 MySQL 实例上执行；将密码与库名按部署环境替换。

-- 1. 创建业务账号（应用连接池使用此账号访问业务表 + 只读审计表）
CREATE USER IF NOT EXISTS 'neuralgate_app'@'%' IDENTIFIED BY '请替换为强口令';

-- 2. 业务表全权（模型配置 / API Key / 上游 / 限流配置）
GRANT SELECT, INSERT, UPDATE, DELETE ON neuralgate.api_keys           TO 'neuralgate_app'@'%';
GRANT SELECT, INSERT, UPDATE, DELETE ON neuralgate.model_configs      TO 'neuralgate_app'@'%';
GRANT SELECT, INSERT, UPDATE, DELETE ON neuralgate.rate_limit_configs TO 'neuralgate_app'@'%';
GRANT SELECT, INSERT, UPDATE, DELETE ON neuralgate.upstreams          TO 'neuralgate_app'@'%';

-- 3. 审计表只读（审计日志 + 篡改告警）：仅 SELECT，无 UPDATE/DELETE
--    注意：留存清理与告警写入须由具备 DML 权限的运维账号执行
GRANT SELECT ON neuralgate.audit_logs            TO 'neuralgate_app'@'%';
GRANT SELECT ON neuralgate.audit_tamper_alerts   TO 'neuralgate_app'@'%';

FLUSH PRIVILEGES;

-- 4. 核验：以业务账号验证无写权限（预期报错 1142/1143）
--    mysql -u neuralgate_app -p -e "UPDATE neuralgate.audit_logs SET model_name='x' WHERE id='y';"
--    mysql -u neuralgate_app -p -e "DELETE FROM neuralgate.audit_logs WHERE id='y';"

-- 5. 如需回收误授权：
--    REVOKE ALL PRIVILEGES ON neuralgate.* FROM 'neuralgate_app'@'%';
--    之后按第 2、3 步重新精确授权。

-- SQLite 替代措施（单文件数据库无账号体系）：
--   1) 应用以独立系统用户运行，数据目录权限 0750、数据库文件 0640；
--   2) 备份与校验使用只读挂载或 dbstat 只读副本；
--   3) 主机层面启用文件完整性监控（如 auditd/AIDE）作为补偿控制。
