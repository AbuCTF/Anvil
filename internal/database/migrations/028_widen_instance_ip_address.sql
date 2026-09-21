-- k8s instances store a player-facing connect string in ip_address
-- (e.g. "ncat --ssl pwn-<id>.pwn.h7tex.com 1337"), which overflows the original
-- varchar(45) sized for IPs and fails the instance insert. widen it to hold a
-- host or a connect command. docker/vm IPs still fit.
ALTER TABLE instances ALTER COLUMN ip_address TYPE VARCHAR(255);
