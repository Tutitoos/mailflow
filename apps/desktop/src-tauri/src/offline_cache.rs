use aes_gcm::{
    Aes256Gcm, Nonce,
    aead::{Aead, KeyInit, Payload},
};
use rusqlite::{Connection, ErrorCode, OptionalExtension, params};
use serde_json::Value;
use sha2::{Digest, Sha256};
use std::{
    collections::HashSet,
    fmt, fs,
    path::{Path, PathBuf},
    sync::{
        Arc, Mutex, MutexGuard,
        atomic::{AtomicBool, Ordering},
    },
    time::{Duration, SystemTime, UNIX_EPOCH},
};

const SCHEMA_VERSION: i64 = 1;
const RETENTION: Duration = Duration::from_secs(90 * 24 * 60 * 60);
const MAX_PAYLOAD_BYTES: usize = 2 * 1024 * 1024;
const MAX_RECORDS_PER_ACCOUNT: i64 = 10_000;
const KEY_BYTES: usize = 32;
const NONCE_BYTES: usize = 12;
#[cfg(target_os = "macos")]
const KEYCHAIN_SERVICE: &str = "dev.tutitoos.mailflow.offline-cache";

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum CacheKind {
    Navigation,
    Inbox,
    Search,
    Conversation,
}

impl CacheKind {
    pub fn parse(value: &str) -> Option<Self> {
        match value {
            "navigation" => Some(Self::Navigation),
            "inbox" => Some(Self::Inbox),
            "search" => Some(Self::Search),
            "conversation" => Some(Self::Conversation),
            _ => None,
        }
    }

    fn as_str(self) -> &'static str {
        match self {
            Self::Navigation => "navigation",
            Self::Inbox => "inbox",
            Self::Search => "search",
            Self::Conversation => "conversation",
        }
    }
}

#[derive(Debug)]
pub enum CacheError {
    Corrupt,
    Crypto,
    InvalidInput,
    Io(std::io::Error),
    Secret,
    Sqlite(rusqlite::Error),
    State,
    UnsupportedSchema,
}

impl fmt::Display for CacheError {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter.write_str(match self {
            Self::Corrupt => "offline cache is corrupt",
            Self::Crypto => "offline cache encryption failed",
            Self::InvalidInput => "offline cache input is invalid",
            Self::Io(_) => "offline cache filesystem operation failed",
            Self::Secret => "offline cache key storage failed",
            Self::Sqlite(_) => "offline cache database operation failed",
            Self::State => "offline cache state is unavailable",
            Self::UnsupportedSchema => "offline cache schema is newer than this application",
        })
    }
}

impl std::error::Error for CacheError {
    fn source(&self) -> Option<&(dyn std::error::Error + 'static)> {
        match self {
            Self::Io(error) => Some(error),
            Self::Sqlite(error) => Some(error),
            _ => None,
        }
    }
}

impl From<std::io::Error> for CacheError {
    fn from(value: std::io::Error) -> Self {
        Self::Io(value)
    }
}

impl From<rusqlite::Error> for CacheError {
    fn from(value: rusqlite::Error) -> Self {
        Self::Sqlite(value)
    }
}

pub trait SecretStore: Send + Sync {
    fn get(&self, account_hash: &str) -> Result<Option<[u8; KEY_BYTES]>, CacheError>;
    fn set(&self, account_hash: &str, key: &[u8; KEY_BYTES]) -> Result<(), CacheError>;
    fn delete(&self, account_hash: &str) -> Result<(), CacheError>;
}

#[derive(Default)]
pub struct PlatformSecrets;

#[cfg(target_os = "macos")]
impl SecretStore for PlatformSecrets {
    fn get(&self, account_hash: &str) -> Result<Option<[u8; KEY_BYTES]>, CacheError> {
        let entry =
            keyring::Entry::new(KEYCHAIN_SERVICE, account_hash).map_err(|_| CacheError::Secret)?;
        let encoded = match entry.get_password() {
            Ok(value) => value,
            Err(keyring::Error::NoEntry) => return Ok(None),
            Err(_) => return Err(CacheError::Secret),
        };
        decode_key(&encoded).map(Some)
    }

    fn set(&self, account_hash: &str, key: &[u8; KEY_BYTES]) -> Result<(), CacheError> {
        keyring::Entry::new(KEYCHAIN_SERVICE, account_hash)
            .and_then(|entry| entry.set_password(&hex::encode(key)))
            .map_err(|_| CacheError::Secret)
    }

    fn delete(&self, account_hash: &str) -> Result<(), CacheError> {
        let entry =
            keyring::Entry::new(KEYCHAIN_SERVICE, account_hash).map_err(|_| CacheError::Secret)?;
        match entry.delete_credential() {
            Ok(()) | Err(keyring::Error::NoEntry) => Ok(()),
            Err(_) => Err(CacheError::Secret),
        }
    }
}

#[cfg(not(target_os = "macos"))]
impl SecretStore for PlatformSecrets {
    fn get(&self, _account_hash: &str) -> Result<Option<[u8; KEY_BYTES]>, CacheError> {
        Err(CacheError::Secret)
    }

    fn set(&self, _account_hash: &str, _key: &[u8; KEY_BYTES]) -> Result<(), CacheError> {
        Err(CacheError::Secret)
    }

    fn delete(&self, _account_hash: &str) -> Result<(), CacheError> {
        Err(CacheError::Secret)
    }
}

pub struct OfflineCache {
    path: PathBuf,
    secrets: Arc<dyn SecretStore>,
    integrity_checked: AtomicBool,
    operations: Mutex<()>,
}

impl OfflineCache {
    pub fn new(path: PathBuf) -> Self {
        Self::with_secrets(path, Arc::new(PlatformSecrets))
    }

    pub fn with_secrets(path: PathBuf, secrets: Arc<dyn SecretStore>) -> Self {
        Self {
            path,
            secrets,
            integrity_checked: AtomicBool::new(false),
            operations: Mutex::new(()),
        }
    }

    pub fn store_accounts(&self, accounts: Vec<Value>) -> Result<(), CacheError> {
        let _operation = self.operation()?;
        self.store_accounts_at(accounts, now_epoch())
    }

    pub fn list_accounts(&self) -> Result<Vec<Value>, CacheError> {
        let _operation = self.operation()?;
        self.list_accounts_at(now_epoch())
    }

    pub fn has_accounts(&self) -> Result<bool, CacheError> {
        let _operation = self.operation()?;
        Ok(!self.list_accounts_at(now_epoch())?.is_empty())
    }

    pub fn write(
        &self,
        account_id: &str,
        kind: CacheKind,
        cache_key: &str,
        value: &Value,
    ) -> Result<(), CacheError> {
        let _operation = self.operation()?;
        self.write_at(account_id, kind, cache_key, value, now_epoch())
    }

    pub fn read(
        &self,
        account_id: &str,
        kind: CacheKind,
        cache_key: &str,
    ) -> Result<Option<Value>, CacheError> {
        let _operation = self.operation()?;
        self.read_at(account_id, kind, cache_key, now_epoch())
    }

    pub fn remove_account(&self, account_id: &str) -> Result<(), CacheError> {
        let _operation = self.operation()?;
        self.remove_account_unlocked(account_id)
    }

    fn remove_account_unlocked(&self, account_id: &str) -> Result<(), CacheError> {
        validate_identifier(account_id)?;
        let account_hash = hash(account_id.as_bytes());
        let account_hex = hex::encode(account_hash);

        // Destroy the only decryption key first. A later SQLite failure can leave only
        // irrecoverable ciphertext, never readable mail data.
        self.secrets.delete(&account_hex)?;
        let connection = self.connection()?;
        connection.execute(
            "DELETE FROM cache_records WHERE account_hash = ?1",
            params![account_hash.as_slice()],
        )?;
        connection.execute_batch("PRAGMA wal_checkpoint(TRUNCATE); VACUUM;")?;
        Ok(())
    }

    fn store_accounts_at(&self, accounts: Vec<Value>, now: i64) -> Result<(), CacheError> {
        let mut incoming = HashSet::new();
        for account in &accounts {
            let account_id = account
                .get("id")
                .and_then(Value::as_str)
                .ok_or(CacheError::InvalidInput)?;
            validate_identifier(account_id)?;
            incoming.insert(account_id.to_owned());
            self.write_value_at(account_id, "account", "profile", account, now)?;
        }

        for cached in self.list_accounts_at(now)? {
            if let Some(account_id) = cached.get("id").and_then(Value::as_str)
                && !incoming.contains(account_id)
            {
                self.remove_account_unlocked(account_id)?;
            }
        }
        Ok(())
    }

    fn list_accounts_at(&self, now: i64) -> Result<Vec<Value>, CacheError> {
        let connection = self.connection()?;
        delete_expired(&connection, now)?;
        let mut statement = connection.prepare(
            "SELECT account_hash, cache_key, nonce, ciphertext
             FROM cache_records WHERE record_kind = 'account' AND expires_at > ?1
             ORDER BY updated_at DESC",
        )?;
        let rows = statement.query_map(params![now], |row| {
            Ok((
                row.get::<_, Vec<u8>>(0)?,
                row.get::<_, Vec<u8>>(1)?,
                row.get::<_, Vec<u8>>(2)?,
                row.get::<_, Vec<u8>>(3)?,
            ))
        })?;

        let mut accounts = Vec::new();
        for row in rows {
            let (account_hash, cache_key, nonce, ciphertext) = row?;
            let account_hex = hex::encode(&account_hash);
            let Some(key) = self.secrets.get(&account_hex)? else {
                continue;
            };
            match decrypt(
                &key,
                &aad(&account_hash, "account", &cache_key),
                &nonce,
                &ciphertext,
            ) {
                Ok(value) => accounts.push(value),
                Err(_) => {
                    connection.execute(
                        "DELETE FROM cache_records WHERE account_hash = ?1 AND record_kind = 'account' AND cache_key = ?2",
                        params![account_hash, cache_key],
                    )?;
                }
            }
        }
        Ok(accounts)
    }

    fn write_at(
        &self,
        account_id: &str,
        kind: CacheKind,
        cache_key: &str,
        value: &Value,
        now: i64,
    ) -> Result<(), CacheError> {
        self.write_value_at(account_id, kind.as_str(), cache_key, value, now)
    }

    fn write_value_at(
        &self,
        account_id: &str,
        kind: &str,
        cache_key: &str,
        value: &Value,
        now: i64,
    ) -> Result<(), CacheError> {
        validate_identifier(account_id)?;
        validate_cache_key(cache_key)?;
        let encoded = serde_json::to_vec(value).map_err(|_| CacheError::InvalidInput)?;
        if encoded.len() > MAX_PAYLOAD_BYTES {
            return Err(CacheError::InvalidInput);
        }

        let account_hash = hash(account_id.as_bytes());
        let account_hex = hex::encode(account_hash);
        let key = self.key_for_write(&account_hex)?;
        let cache_hash = hash(cache_key.as_bytes());
        let (nonce, ciphertext) = encrypt(
            &key,
            &aad(account_hash.as_slice(), kind, cache_hash.as_slice()),
            &encoded,
        )?;
        let expires_at = now + RETENTION.as_secs() as i64;
        let connection = self.connection()?;
        delete_expired(&connection, now)?;
        connection.execute(
            "INSERT INTO cache_records
               (account_hash, record_kind, cache_key, nonce, ciphertext, updated_at, expires_at)
             VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7)
             ON CONFLICT(account_hash, record_kind, cache_key) DO UPDATE SET
               nonce = excluded.nonce,
               ciphertext = excluded.ciphertext,
               updated_at = excluded.updated_at,
               expires_at = excluded.expires_at",
            params![
                account_hash.as_slice(),
                kind,
                cache_hash.as_slice(),
                nonce.as_slice(),
                ciphertext,
                now,
                expires_at
            ],
        )?;
        connection.execute(
            "DELETE FROM cache_records WHERE (account_hash, record_kind, cache_key) IN (
               SELECT account_hash, record_kind, cache_key FROM cache_records
               WHERE account_hash = ?1 AND record_kind != 'account'
               ORDER BY updated_at DESC, record_kind DESC, cache_key DESC
               LIMIT -1 OFFSET ?2
             )",
            params![account_hash.as_slice(), MAX_RECORDS_PER_ACCOUNT],
        )?;
        Ok(())
    }

    fn read_at(
        &self,
        account_id: &str,
        kind: CacheKind,
        cache_key: &str,
        now: i64,
    ) -> Result<Option<Value>, CacheError> {
        validate_identifier(account_id)?;
        validate_cache_key(cache_key)?;
        let account_hash = hash(account_id.as_bytes());
        let account_hex = hex::encode(account_hash);
        let cache_hash = hash(cache_key.as_bytes());
        let connection = self.connection()?;
        delete_expired(&connection, now)?;
        let encrypted = connection
            .query_row(
                "SELECT nonce, ciphertext FROM cache_records
                 WHERE account_hash = ?1 AND record_kind = ?2 AND cache_key = ?3 AND expires_at > ?4",
                params![account_hash.as_slice(), kind.as_str(), cache_hash.as_slice(), now],
                |row| Ok((row.get::<_, Vec<u8>>(0)?, row.get::<_, Vec<u8>>(1)?)),
            )
            .optional()?;
        let Some((nonce, ciphertext)) = encrypted else {
            return Ok(None);
        };
        let Some(key) = self.secrets.get(&account_hex)? else {
            return Ok(None);
        };
        match decrypt(
            &key,
            &aad(
                account_hash.as_slice(),
                kind.as_str(),
                cache_hash.as_slice(),
            ),
            &nonce,
            &ciphertext,
        ) {
            Ok(value) => Ok(Some(value)),
            Err(_) => {
                connection.execute(
                    "DELETE FROM cache_records WHERE account_hash = ?1 AND record_kind = ?2 AND cache_key = ?3",
                    params![account_hash.as_slice(), kind.as_str(), cache_hash.as_slice()],
                )?;
                Ok(None)
            }
        }
    }

    fn key_for_write(&self, account_hash: &str) -> Result<[u8; KEY_BYTES], CacheError> {
        if let Some(key) = self.secrets.get(account_hash)? {
            return Ok(key);
        }
        let mut key = [0_u8; KEY_BYTES];
        getrandom::fill(&mut key).map_err(|_| CacheError::Crypto)?;
        self.secrets.set(account_hash, &key)?;
        Ok(key)
    }

    fn connection(&self) -> Result<Connection, CacheError> {
        match self.try_connection() {
            Ok(connection) => Ok(connection),
            Err(error) if error.is_corruption() => {
                self.integrity_checked.store(false, Ordering::Release);
                remove_database_files(&self.path)?;
                self.try_connection()
            }
            Err(error) => Err(error),
        }
    }

    fn operation(&self) -> Result<MutexGuard<'_, ()>, CacheError> {
        self.operations.lock().map_err(|_| CacheError::State)
    }

    fn try_connection(&self) -> Result<Connection, CacheError> {
        if let Some(parent) = self.path.parent() {
            fs::create_dir_all(parent)?;
        }
        ensure_database_file(&self.path)?;
        let mut connection = Connection::open(&self.path)?;
        connection.busy_timeout(Duration::from_secs(5))?;
        connection.execute_batch(
            "PRAGMA journal_mode = WAL;
             PRAGMA synchronous = FULL;
             PRAGMA secure_delete = ON;
             PRAGMA foreign_keys = ON;",
        )?;
        if !self.integrity_checked.load(Ordering::Acquire) {
            let integrity: String =
                connection.query_row("PRAGMA quick_check", [], |row| row.get(0))?;
            if integrity != "ok" {
                return Err(CacheError::Corrupt);
            }
        }
        migrate(&mut connection)?;
        secure_permissions(&self.path)?;
        self.integrity_checked.store(true, Ordering::Release);
        Ok(connection)
    }
}

impl CacheError {
    fn is_corruption(&self) -> bool {
        match self {
            Self::Corrupt => true,
            Self::Sqlite(rusqlite::Error::SqliteFailure(failure, _)) => matches!(
                failure.code,
                ErrorCode::DatabaseCorrupt | ErrorCode::NotADatabase
            ),
            _ => false,
        }
    }
}

fn migrate(connection: &mut Connection) -> Result<(), CacheError> {
    let version: i64 = connection.query_row("PRAGMA user_version", [], |row| row.get(0))?;
    if version > SCHEMA_VERSION {
        return Err(CacheError::UnsupportedSchema);
    }
    if version == 0 {
        let transaction = connection.transaction()?;
        transaction.execute_batch(
            "CREATE TABLE cache_records (
               account_hash BLOB NOT NULL CHECK(length(account_hash) = 32),
               record_kind TEXT NOT NULL CHECK(record_kind IN ('account', 'navigation', 'inbox', 'search', 'conversation')),
               cache_key BLOB NOT NULL CHECK(length(cache_key) = 32),
               nonce BLOB NOT NULL CHECK(length(nonce) = 12),
               ciphertext BLOB NOT NULL,
               updated_at INTEGER NOT NULL,
               expires_at INTEGER NOT NULL,
               PRIMARY KEY (account_hash, record_kind, cache_key)
             );
             CREATE INDEX cache_records_expiry_idx ON cache_records(expires_at);
             PRAGMA user_version = 1;",
        )?;
        transaction.commit()?;
    }
    Ok(())
}

fn delete_expired(connection: &Connection, now: i64) -> Result<(), CacheError> {
    connection.execute(
        "DELETE FROM cache_records WHERE expires_at <= ?1",
        params![now],
    )?;
    Ok(())
}

fn encrypt(
    key: &[u8; KEY_BYTES],
    associated_data: &[u8],
    plaintext: &[u8],
) -> Result<([u8; NONCE_BYTES], Vec<u8>), CacheError> {
    let cipher = Aes256Gcm::new_from_slice(key).map_err(|_| CacheError::Crypto)?;
    let mut nonce = [0_u8; NONCE_BYTES];
    getrandom::fill(&mut nonce).map_err(|_| CacheError::Crypto)?;
    let ciphertext = cipher
        .encrypt(
            Nonce::from_slice(&nonce),
            Payload {
                msg: plaintext,
                aad: associated_data,
            },
        )
        .map_err(|_| CacheError::Crypto)?;
    Ok((nonce, ciphertext))
}

fn decrypt(
    key: &[u8; KEY_BYTES],
    associated_data: &[u8],
    nonce: &[u8],
    ciphertext: &[u8],
) -> Result<Value, CacheError> {
    if nonce.len() != NONCE_BYTES {
        return Err(CacheError::Corrupt);
    }
    let cipher = Aes256Gcm::new_from_slice(key).map_err(|_| CacheError::Crypto)?;
    let plaintext = cipher
        .decrypt(
            Nonce::from_slice(nonce),
            Payload {
                msg: ciphertext,
                aad: associated_data,
            },
        )
        .map_err(|_| CacheError::Corrupt)?;
    serde_json::from_slice(&plaintext).map_err(|_| CacheError::Corrupt)
}

fn aad(account_hash: &[u8], kind: &str, cache_key: &[u8]) -> Vec<u8> {
    let mut value = Vec::with_capacity(account_hash.len() + kind.len() + cache_key.len() + 2);
    value.extend_from_slice(account_hash);
    value.push(0);
    value.extend_from_slice(kind.as_bytes());
    value.push(0);
    value.extend_from_slice(cache_key);
    value
}

fn hash(value: &[u8]) -> [u8; 32] {
    Sha256::digest(value).into()
}

fn validate_identifier(value: &str) -> Result<(), CacheError> {
    if value.is_empty() || value.len() > 128 || value.chars().any(char::is_control) {
        return Err(CacheError::InvalidInput);
    }
    Ok(())
}

fn validate_cache_key(value: &str) -> Result<(), CacheError> {
    if value.is_empty() || value.len() > 1024 || value.chars().any(char::is_control) {
        return Err(CacheError::InvalidInput);
    }
    Ok(())
}

#[cfg(target_os = "macos")]
fn decode_key(encoded: &str) -> Result<[u8; KEY_BYTES], CacheError> {
    let bytes = hex::decode(encoded).map_err(|_| CacheError::Secret)?;
    bytes.try_into().map_err(|_| CacheError::Secret)
}

fn now_epoch() -> i64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .unwrap_or_default()
        .as_secs() as i64
}

fn remove_database_files(path: &Path) -> Result<(), CacheError> {
    for candidate in [
        path.to_path_buf(),
        PathBuf::from(format!("{}-wal", path.display())),
        PathBuf::from(format!("{}-shm", path.display())),
    ] {
        match fs::remove_file(candidate) {
            Ok(()) => {}
            Err(error) if error.kind() == std::io::ErrorKind::NotFound => {}
            Err(error) => return Err(CacheError::Io(error)),
        }
    }
    Ok(())
}

#[cfg(unix)]
fn ensure_database_file(path: &Path) -> Result<(), CacheError> {
    use std::{fs::OpenOptions, os::unix::fs::OpenOptionsExt};
    match fs::symlink_metadata(path) {
        Ok(metadata) if metadata.file_type().is_symlink() => return Err(CacheError::InvalidInput),
        Ok(_) => secure_permissions(path)?,
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => {
            match OpenOptions::new()
                .write(true)
                .create_new(true)
                .mode(0o600)
                .open(path)
            {
                Ok(_) => {}
                Err(error) if error.kind() == std::io::ErrorKind::AlreadyExists => {
                    secure_permissions(path)?;
                }
                Err(error) => return Err(CacheError::Io(error)),
            }
        }
        Err(error) => return Err(CacheError::Io(error)),
    }
    Ok(())
}

#[cfg(not(unix))]
fn ensure_database_file(_path: &Path) -> Result<(), CacheError> {
    Ok(())
}

#[cfg(unix)]
fn secure_permissions(path: &Path) -> Result<(), CacheError> {
    use std::os::unix::fs::PermissionsExt;
    fs::set_permissions(path, fs::Permissions::from_mode(0o600))?;
    Ok(())
}

#[cfg(not(unix))]
fn secure_permissions(_path: &Path) -> Result<(), CacheError> {
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::{collections::HashMap, sync::Mutex};
    use tempfile::tempdir;

    #[derive(Default)]
    struct MemorySecrets(Mutex<HashMap<String, [u8; KEY_BYTES]>>);

    impl SecretStore for MemorySecrets {
        fn get(&self, account_hash: &str) -> Result<Option<[u8; KEY_BYTES]>, CacheError> {
            Ok(self.0.lock().unwrap().get(account_hash).copied())
        }

        fn set(&self, account_hash: &str, key: &[u8; KEY_BYTES]) -> Result<(), CacheError> {
            self.0.lock().unwrap().insert(account_hash.to_owned(), *key);
            Ok(())
        }

        fn delete(&self, account_hash: &str) -> Result<(), CacheError> {
            self.0.lock().unwrap().remove(account_hash);
            Ok(())
        }
    }

    fn fixture() -> (tempfile::TempDir, Arc<MemorySecrets>, OfflineCache) {
        let directory = tempdir().unwrap();
        let secrets = Arc::new(MemorySecrets::default());
        let cache = OfflineCache::with_secrets(
            directory.path().join("offline-cache.sqlite3"),
            secrets.clone(),
        );
        (directory, secrets, cache)
    }

    #[test]
    fn payloads_are_encrypted_and_partitioned_by_account() {
        let (directory, _, cache) = fixture();
        let private = serde_json::json!({"subject":"fixture-private-subject"});
        cache
            .write("account-a", CacheKind::Inbox, "primary:first", &private)
            .unwrap();
        assert_eq!(
            cache
                .read("account-a", CacheKind::Inbox, "primary:first")
                .unwrap(),
            Some(private)
        );
        assert_eq!(
            cache
                .read("account-b", CacheKind::Inbox, "primary:first")
                .unwrap(),
            None
        );
        let database = fs::read(directory.path().join("offline-cache.sqlite3")).unwrap();
        assert!(
            !database
                .windows(b"fixture-private-subject".len())
                .any(|window| window == b"fixture-private-subject")
        );
        assert!(
            !database
                .windows(b"account-a".len())
                .any(|window| window == b"account-a")
        );
        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;
            let mode = fs::metadata(directory.path().join("offline-cache.sqlite3"))
                .unwrap()
                .permissions()
                .mode();
            assert_eq!(mode & 0o777, 0o600);
        }
    }

    #[test]
    fn concurrent_first_writes_share_one_account_key() {
        let (_directory, _, cache) = fixture();
        let cache = Arc::new(cache);
        let mut writes = Vec::new();
        for index in 0..16 {
            let cache = Arc::clone(&cache);
            writes.push(std::thread::spawn(move || {
                cache
                    .write(
                        "account-a",
                        CacheKind::Conversation,
                        &format!("thread-{index}"),
                        &serde_json::json!({"index":index}),
                    )
                    .unwrap();
            }));
        }
        for write in writes {
            write.join().unwrap();
        }
        for index in 0..16 {
            assert_eq!(
                cache
                    .read(
                        "account-a",
                        CacheKind::Conversation,
                        &format!("thread-{index}"),
                    )
                    .unwrap(),
                Some(serde_json::json!({"index":index}))
            );
        }
    }

    #[test]
    fn account_removal_destroys_rows_and_its_key() {
        let (_, secrets, cache) = fixture();
        cache
            .store_accounts(vec![
                serde_json::json!({"id":"account-a","provider":"google"}),
            ])
            .unwrap();
        cache
            .write(
                "account-a",
                CacheKind::Conversation,
                "thread-a:first",
                &serde_json::json!({"messages":[]}),
            )
            .unwrap();
        let account_hex = hex::encode(hash(b"account-a"));

        cache.remove_account("account-a").unwrap();

        assert!(secrets.get(&account_hex).unwrap().is_none());
        assert!(cache.list_accounts().unwrap().is_empty());
        assert!(
            cache
                .read("account-a", CacheKind::Conversation, "thread-a:first")
                .unwrap()
                .is_none()
        );
    }

    #[test]
    fn expired_entries_are_evicted_without_remote_mutation() {
        let (_, _, cache) = fixture();
        cache
            .write_at(
                "account-a",
                CacheKind::Search,
                "from:fixture:first",
                &serde_json::json!({"items":[]}),
                10,
            )
            .unwrap();
        assert!(
            cache
                .read_at("account-a", CacheKind::Search, "from:fixture:first", 11)
                .unwrap()
                .is_some()
        );
        assert!(
            cache
                .read_at(
                    "account-a",
                    CacheKind::Search,
                    "from:fixture:first",
                    10 + RETENTION.as_secs() as i64,
                )
                .unwrap()
                .is_none()
        );
    }

    #[test]
    fn cache_keeps_only_the_most_recent_records_per_account() {
        let (_, _, cache) = fixture();
        let account_hash = hash(b"account-a");
        let connection = cache.connection().unwrap();
        connection
            .execute(
                "WITH RECURSIVE sequence(value) AS (
                   SELECT 1
                   UNION ALL
                   SELECT value + 1 FROM sequence WHERE value < ?2
                 )
                 INSERT INTO cache_records
                   (account_hash, record_kind, cache_key, nonce, ciphertext, updated_at, expires_at)
                 SELECT ?1, 'search', randomblob(32), randomblob(12), x'00', value, 9999999999
                 FROM sequence",
                params![account_hash.as_slice(), MAX_RECORDS_PER_ACCOUNT + 1],
            )
            .unwrap();
        drop(connection);

        cache
            .write_at(
                "account-a",
                CacheKind::Inbox,
                "primary:first",
                &serde_json::json!({"items":[]}),
                MAX_RECORDS_PER_ACCOUNT + 2,
            )
            .unwrap();

        let connection = cache.connection().unwrap();
        let count: i64 = connection
            .query_row(
                "SELECT COUNT(*) FROM cache_records
                 WHERE account_hash = ?1 AND record_kind != 'account'",
                params![account_hash.as_slice()],
                |row| row.get(0),
            )
            .unwrap();
        assert_eq!(count, MAX_RECORDS_PER_ACCOUNT);
    }

    #[test]
    fn corrupt_database_is_rebuilt_as_an_empty_cache() {
        let (directory, _, cache) = fixture();
        fs::write(
            directory.path().join("offline-cache.sqlite3"),
            b"not a sqlite database",
        )
        .unwrap();
        assert!(cache.list_accounts().unwrap().is_empty());
        let connection = Connection::open(directory.path().join("offline-cache.sqlite3")).unwrap();
        let version: i64 = connection
            .query_row("PRAGMA user_version", [], |row| row.get(0))
            .unwrap();
        assert_eq!(version, SCHEMA_VERSION);
    }

    #[test]
    fn invalid_ciphertext_is_deleted_instead_of_returned() {
        let (_, _, cache) = fixture();
        cache
            .write(
                "account-a",
                CacheKind::Inbox,
                "primary:first",
                &serde_json::json!({"items":[]}),
            )
            .unwrap();
        let connection = cache.connection().unwrap();
        connection
            .execute("UPDATE cache_records SET ciphertext = x'00'", [])
            .unwrap();
        assert!(
            cache
                .read("account-a", CacheKind::Inbox, "primary:first")
                .unwrap()
                .is_none()
        );
    }
}
