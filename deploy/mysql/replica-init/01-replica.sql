-- Runs once, on an empty replica volume. Replication starts on the next server
-- start, so the primary never waits for this server. Keep it asynchronous:
-- semi-sync would make chat writes depend on the replica.
CHANGE REPLICATION SOURCE TO
    SOURCE_HOST = 'mysql',
    SOURCE_USER = 'repl',
    SOURCE_PASSWORD = 'repl',
    SOURCE_AUTO_POSITION = 1,
    GET_SOURCE_PUBLIC_KEY = 1,
    SOURCE_CONNECT_RETRY = 2,
    SOURCE_RETRY_COUNT = 100000;

SET PERSIST super_read_only = ON;
