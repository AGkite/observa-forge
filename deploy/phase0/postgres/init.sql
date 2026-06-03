-- Phase 0: 元数据表骨架（Phase 2 observa-control 会扩展）
CREATE TABLE IF NOT EXISTS devices (
    id          BIGSERIAL PRIMARY KEY,
    device_code VARCHAR(64)  NOT NULL UNIQUE,
    name        VARCHAR(256) NOT NULL,
    node_class  VARCHAR(128),
    endpoint    VARCHAR(256),
    site        VARCHAR(128),
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

INSERT INTO devices (device_code, name, node_class, endpoint, site)
VALUES ('lab-snmpsim-01', 'SNMP Simulator', 'snmp-generic', '127.0.0.1:1161', 'lab')
ON CONFLICT (device_code) DO NOTHING;
