use reqwest::{
    Client, StatusCode, Url,
    header::{COOKIE, SET_COOKIE},
};
use serde::{Deserialize, Serialize};
use std::{fmt, sync::Arc};
use tokio::sync::Mutex;

const SESSION_SERVICE: &str = "dev.tutitoos.mailflow.native-session";
const SESSION_ACCOUNT: &str = "refresh-session";
const INSTALLATION_ACCOUNT: &str = "installation-id";
const MAX_SESSION_BYTES: usize = 4096;
const MAX_PASSKEY_RESPONSE_BYTES: usize = 64 * 1024;

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum NativeSessionError {
    Invalid,
    Network,
    Revoked,
    Secret,
}

impl fmt::Display for NativeSessionError {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter.write_str(match self {
            Self::Invalid => "native session is invalid",
            Self::Network => "native session service is unavailable",
            Self::Revoked => "native session was revoked",
            Self::Secret => "native session keychain is unavailable",
        })
    }
}

pub trait NativeSecretStore: Send + Sync {
    fn get(&self, account: &str) -> Result<Option<String>, NativeSessionError>;
    fn set(&self, account: &str, secret: &str) -> Result<(), NativeSessionError>;
    fn delete(&self, account: &str) -> Result<(), NativeSessionError>;
}

#[derive(Default)]
pub struct PlatformNativeSecrets;

#[cfg(target_os = "macos")]
impl NativeSecretStore for PlatformNativeSecrets {
    fn get(&self, account: &str) -> Result<Option<String>, NativeSessionError> {
        let entry = keyring::Entry::new(SESSION_SERVICE, account)
            .map_err(|_| NativeSessionError::Secret)?;
        match entry.get_password() {
            Ok(value) => Ok(Some(value)),
            Err(keyring::Error::NoEntry) => Ok(None),
            Err(_) => Err(NativeSessionError::Secret),
        }
    }

    fn set(&self, account: &str, secret: &str) -> Result<(), NativeSessionError> {
        keyring::Entry::new(SESSION_SERVICE, account)
            .and_then(|entry| entry.set_password(secret))
            .map_err(|_| NativeSessionError::Secret)
    }

    fn delete(&self, account: &str) -> Result<(), NativeSessionError> {
        let entry = keyring::Entry::new(SESSION_SERVICE, account)
            .map_err(|_| NativeSessionError::Secret)?;
        match entry.delete_credential() {
            Ok(()) | Err(keyring::Error::NoEntry) => Ok(()),
            Err(_) => Err(NativeSessionError::Secret),
        }
    }
}

#[cfg(not(target_os = "macos"))]
impl NativeSecretStore for PlatformNativeSecrets {
    fn get(&self, _account: &str) -> Result<Option<String>, NativeSessionError> {
        Err(NativeSessionError::Secret)
    }
    fn set(&self, _account: &str, _secret: &str) -> Result<(), NativeSessionError> {
        Err(NativeSessionError::Secret)
    }
    fn delete(&self, _account: &str) -> Result<(), NativeSessionError> {
        Err(NativeSessionError::Secret)
    }
}

#[derive(Debug, Deserialize)]
struct SessionResponse {
    user: SessionUser,
}

#[derive(Debug, Deserialize)]
struct SessionUser {
    locale: Option<String>,
}

#[derive(Debug, Deserialize)]
struct TokenResponse {
    token: String,
}

#[derive(Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct NativeSession {
    pub locale: String,
    pub access_token: String,
}

pub struct NativeSessionManager {
    origin: Url,
    client: Client,
    secrets: Arc<dyn NativeSecretStore>,
    operation: Mutex<()>,
    passkey_challenge: Mutex<Option<String>>,
}

impl NativeSessionManager {
    pub fn new(origin: Url) -> Result<Self, NativeSessionError> {
        Self::with_secrets(origin, Arc::new(PlatformNativeSecrets))
    }

    pub fn with_secrets(
        origin: Url,
        secrets: Arc<dyn NativeSecretStore>,
    ) -> Result<Self, NativeSessionError> {
        let client = Client::builder()
            .redirect(reqwest::redirect::Policy::none())
            .timeout(std::time::Duration::from_secs(15))
            .build()
            .map_err(|_| NativeSessionError::Network)?;
        Ok(Self {
            origin,
            client,
            secrets,
            operation: Mutex::new(()),
            passkey_challenge: Mutex::new(None),
        })
    }

    pub async fn activate(
        &self,
        session_token: String,
    ) -> Result<NativeSession, NativeSessionError> {
        validate_session_token(&session_token)?;
        let _operation = self.operation.lock().await;
        let session = self.exchange(&session_token).await?;
        self.secrets.set(SESSION_ACCOUNT, &session_token)?;
        Ok(session)
    }

    pub async fn current(&self) -> Result<Option<NativeSession>, NativeSessionError> {
        let _operation = self.operation.lock().await;
        let Some(session_token) = self.secrets.get(SESSION_ACCOUNT)? else {
            return Ok(None);
        };
        match self.exchange(&session_token).await {
            Ok(session) => Ok(Some(session)),
            Err(NativeSessionError::Revoked | NativeSessionError::Invalid) => {
                self.secrets.delete(SESSION_ACCOUNT)?;
                Ok(None)
            }
            Err(error) => Err(error),
        }
    }

    pub async fn logout(&self) -> Result<(), NativeSessionError> {
        let _operation = self.operation.lock().await;
        *self.passkey_challenge.lock().await = None;
        let mut remote_result = Ok(());
        if let Some(session_token) = self.secrets.get(SESSION_ACCOUNT)? {
            match (self.installation_id(), self.endpoint("/api/auth/sign-out")) {
                (Ok(installation), Ok(endpoint)) => {
                    match self
                        .client
                        .post(endpoint)
                        .bearer_auth(&session_token)
                        .header("x-mailflow-installation", installation)
                        .send()
                        .await
                    {
                        Ok(response)
                            if response.status().is_success()
                                || response.status() == StatusCode::UNAUTHORIZED => {}
                        Ok(_) | Err(_) => remote_result = Err(NativeSessionError::Network),
                    }
                }
                _ => remote_result = Err(NativeSessionError::Network),
            }
        }
        self.secrets.delete(SESSION_ACCOUNT)?;
        remote_result
    }

    pub async fn forget_local(&self) -> Result<(), NativeSessionError> {
        let _operation = self.operation.lock().await;
        *self.passkey_challenge.lock().await = None;
        self.secrets.delete(SESSION_ACCOUNT)
    }

    pub async fn identity(&self) -> Result<String, NativeSessionError> {
        let _operation = self.operation.lock().await;
        self.installation_id()
    }

    pub async fn begin_passkey(
        &self,
        name: String,
    ) -> Result<serde_json::Value, NativeSessionError> {
        validate_passkey_name(&name)?;
        let _operation = self.operation.lock().await;
        let session_token = self
            .secrets
            .get(SESSION_ACCOUNT)?
            .ok_or(NativeSessionError::Revoked)?;
        let installation = self.installation_id()?;
        let mut endpoint = self.endpoint("/api/auth/passkey/generate-register-options")?;
        endpoint.query_pairs_mut().append_pair("name", &name);
        let response = self
            .client
            .get(endpoint)
            .bearer_auth(session_token)
            .header("x-mailflow-installation", installation)
            .send()
            .await
            .map_err(|_| NativeSessionError::Network)?;
        if response.status() == StatusCode::UNAUTHORIZED {
            self.secrets.delete(SESSION_ACCOUNT)?;
            return Err(NativeSessionError::Revoked);
        }
        if !response.status().is_success() {
            return Err(NativeSessionError::Network);
        }
        let challenge = response
            .headers()
            .get_all(SET_COOKIE)
            .iter()
            .filter_map(|value| value.to_str().ok())
            .find(|cookie| {
                cookie
                    .split(';')
                    .next()
                    .is_some_and(|part| part.contains("passkey"))
            })
            .and_then(|cookie| cookie.split(';').next())
            .map(str::to_owned)
            .ok_or(NativeSessionError::Invalid)?;
        let options = response
            .json::<serde_json::Value>()
            .await
            .map_err(|_| NativeSessionError::Invalid)?;
        *self.passkey_challenge.lock().await = Some(challenge);
        Ok(options)
    }

    pub async fn finish_passkey(
        &self,
        name: String,
        response: serde_json::Value,
    ) -> Result<(), NativeSessionError> {
        validate_passkey_name(&name)?;
        if !response.is_object()
            || serde_json::to_vec(&response)
                .map_err(|_| NativeSessionError::Invalid)?
                .len()
                > MAX_PASSKEY_RESPONSE_BYTES
        {
            return Err(NativeSessionError::Invalid);
        }
        let _operation = self.operation.lock().await;
        let session_token = self
            .secrets
            .get(SESSION_ACCOUNT)?
            .ok_or(NativeSessionError::Revoked)?;
        let challenge = self
            .passkey_challenge
            .lock()
            .await
            .take()
            .ok_or(NativeSessionError::Invalid)?;
        let installation = self.installation_id()?;
        let result = self
            .client
            .post(self.endpoint("/api/auth/passkey/verify-registration")?)
            .bearer_auth(session_token)
            .header("x-mailflow-installation", installation)
            .header(COOKIE, challenge)
            .json(&serde_json::json!({ "response": response, "name": name }))
            .send()
            .await
            .map_err(|_| NativeSessionError::Network)?;
        if result.status() == StatusCode::UNAUTHORIZED {
            self.secrets.delete(SESSION_ACCOUNT)?;
            return Err(NativeSessionError::Revoked);
        }
        if !result.status().is_success() {
            return Err(NativeSessionError::Invalid);
        }
        Ok(())
    }

    async fn exchange(&self, session_token: &str) -> Result<NativeSession, NativeSessionError> {
        let installation = self.installation_id()?;
        let session_response = self
            .client
            .get(self.endpoint("/api/auth/get-session")?)
            .bearer_auth(session_token)
            .header("x-mailflow-installation", &installation)
            .send()
            .await
            .map_err(|_| NativeSessionError::Network)?;
        if session_response.status() == StatusCode::UNAUTHORIZED {
            return Err(NativeSessionError::Revoked);
        }
        if !session_response.status().is_success() {
            return Err(NativeSessionError::Network);
        }
        let session = session_response
            .json::<SessionResponse>()
            .await
            .map_err(|_| NativeSessionError::Invalid)?;
        let token_response = self
            .client
            .get(self.endpoint("/api/auth/token")?)
            .bearer_auth(session_token)
            .header("x-mailflow-installation", installation)
            .send()
            .await
            .map_err(|_| NativeSessionError::Network)?;
        if token_response.status() == StatusCode::UNAUTHORIZED {
            return Err(NativeSessionError::Revoked);
        }
        if !token_response.status().is_success() {
            return Err(NativeSessionError::Network);
        }
        let token = token_response
            .json::<TokenResponse>()
            .await
            .map_err(|_| NativeSessionError::Invalid)?
            .token;
        if token.len() > MAX_SESSION_BYTES || token.split('.').count() != 3 {
            return Err(NativeSessionError::Invalid);
        }
        Ok(NativeSession {
            locale: if session.user.locale.as_deref() == Some("es") {
                "es"
            } else {
                "en"
            }
            .to_owned(),
            access_token: token,
        })
    }

    fn installation_id(&self) -> Result<String, NativeSessionError> {
        if let Some(identifier) = self.secrets.get(INSTALLATION_ACCOUNT)? {
            if identifier.len() == 64 && identifier.bytes().all(|byte| byte.is_ascii_hexdigit()) {
                return Ok(identifier.to_ascii_lowercase());
            }
            self.secrets.delete(INSTALLATION_ACCOUNT)?;
        }
        let mut bytes = [0_u8; 32];
        getrandom::fill(&mut bytes).map_err(|_| NativeSessionError::Secret)?;
        let identifier = hex::encode(bytes);
        self.secrets.set(INSTALLATION_ACCOUNT, &identifier)?;
        Ok(identifier)
    }

    fn endpoint(&self, path: &str) -> Result<Url, NativeSessionError> {
        self.origin
            .join(path)
            .map_err(|_| NativeSessionError::Invalid)
    }
}

fn validate_session_token(token: &str) -> Result<(), NativeSessionError> {
    if token.len() < 32
        || token.len() > MAX_SESSION_BYTES
        || token.chars().any(char::is_whitespace)
        || token.split('.').count() != 2
    {
        return Err(NativeSessionError::Invalid);
    }
    Ok(())
}

fn validate_passkey_name(name: &str) -> Result<(), NativeSessionError> {
    if name.trim() != name || name.is_empty() || name.len() > 64 {
        return Err(NativeSessionError::Invalid);
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::{collections::HashMap, sync::Mutex as StdMutex};

    #[derive(Default)]
    struct MemorySecrets(StdMutex<HashMap<String, String>>);

    impl NativeSecretStore for MemorySecrets {
        fn get(&self, account: &str) -> Result<Option<String>, NativeSessionError> {
            Ok(self.0.lock().unwrap().get(account).cloned())
        }
        fn set(&self, account: &str, secret: &str) -> Result<(), NativeSessionError> {
            self.0
                .lock()
                .unwrap()
                .insert(account.to_owned(), secret.to_owned());
            Ok(())
        }
        fn delete(&self, account: &str) -> Result<(), NativeSessionError> {
            self.0.lock().unwrap().remove(account);
            Ok(())
        }
    }

    struct LockedSecrets;

    impl NativeSecretStore for LockedSecrets {
        fn get(&self, _account: &str) -> Result<Option<String>, NativeSessionError> {
            Err(NativeSessionError::Secret)
        }
        fn set(&self, _account: &str, _secret: &str) -> Result<(), NativeSessionError> {
            Err(NativeSessionError::Secret)
        }
        fn delete(&self, _account: &str) -> Result<(), NativeSessionError> {
            Err(NativeSessionError::Secret)
        }
    }

    #[test]
    fn session_material_is_bounded_and_signed() {
        assert!(validate_session_token(&format!("{}.signature", "a".repeat(32))).is_ok());
        assert_eq!(
            validate_session_token("unsigned"),
            Err(NativeSessionError::Invalid)
        );
        assert_eq!(
            validate_session_token(&format!("{}.signature", "a".repeat(MAX_SESSION_BYTES))),
            Err(NativeSessionError::Invalid)
        );
    }

    #[test]
    fn passkey_labels_are_bounded() {
        assert!(validate_passkey_name("Mailflow").is_ok());
        assert_eq!(validate_passkey_name(""), Err(NativeSessionError::Invalid));
        assert_eq!(
            validate_passkey_name(" padded "),
            Err(NativeSessionError::Invalid)
        );
        assert_eq!(
            validate_passkey_name(&"a".repeat(65)),
            Err(NativeSessionError::Invalid)
        );
    }

    #[test]
    fn installation_identity_is_stable_and_kept_in_secret_store() {
        let secrets = Arc::new(MemorySecrets::default());
        let manager = NativeSessionManager::with_secrets(
            Url::parse("https://mail.example.test").unwrap(),
            secrets.clone(),
        )
        .unwrap();
        let first = manager.installation_id().unwrap();
        assert_eq!(first.len(), 64);
        assert_eq!(manager.installation_id().unwrap(), first);
        let reinstalled = NativeSessionManager::with_secrets(
            Url::parse("https://mail.example.test").unwrap(),
            secrets.clone(),
        )
        .unwrap();
        assert_eq!(reinstalled.installation_id().unwrap(), first);
        assert_eq!(secrets.get(INSTALLATION_ACCOUNT).unwrap(), Some(first));
    }

    #[test]
    fn corrupted_installation_identity_is_replaced() {
        let secrets = Arc::new(MemorySecrets::default());
        secrets
            .set(INSTALLATION_ACCOUNT, "corrupt installation identity")
            .unwrap();
        let manager = NativeSessionManager::with_secrets(
            Url::parse("https://mail.example.test").unwrap(),
            secrets.clone(),
        )
        .unwrap();

        let repaired = manager.installation_id().unwrap();

        assert_eq!(repaired.len(), 64);
        assert!(repaired.bytes().all(|byte| byte.is_ascii_hexdigit()));
        assert_eq!(secrets.get(INSTALLATION_ACCOUNT).unwrap(), Some(repaired));
    }

    #[test]
    fn locked_keychain_fails_closed() {
        let manager = NativeSessionManager::with_secrets(
            Url::parse("https://mail.example.test").unwrap(),
            Arc::new(LockedSecrets),
        )
        .unwrap();

        assert_eq!(manager.installation_id(), Err(NativeSessionError::Secret));
    }
}
