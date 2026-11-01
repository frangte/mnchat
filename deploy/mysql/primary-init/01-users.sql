-- Runs once, on an empty primary volume, before migrations. Users replicate to
-- the replica. Grants on views live in migrations, because granting on a view
-- that doesn't exist yet fails and stops the server from starting.
CREATE USER 'repl'@'%' IDENTIFIED BY 'repl';
GRANT REPLICATION SLAVE ON *.* TO 'repl'@'%';

CREATE USER 'search_reader'@'%' IDENTIFIED BY 'search_reader';

CREATE USER 'exporter'@'%' IDENTIFIED BY 'exporter';
GRANT PROCESS, REPLICATION CLIENT, SELECT ON *.* TO 'exporter'@'%';

GRANT ALL ON mnchat.* TO 'mnchat'@'%' WITH GRANT OPTION;
