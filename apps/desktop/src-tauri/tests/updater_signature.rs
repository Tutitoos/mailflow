use base64::{Engine as _, engine::general_purpose::STANDARD};
use minisign_verify::{PublicKey, Signature};
use std::{env, fs};

fn release_input(name: &str) -> Option<String> {
    env::var(name).ok().filter(|value| !value.is_empty())
}

#[test]
fn updater_artifact_accepts_only_its_signed_bytes() {
    let Some(artifact_path) = release_input("MAILFLOW_TEST_UPDATE_ARTIFACT") else {
        return;
    };
    let signature_path = release_input("MAILFLOW_TEST_UPDATE_SIGNATURE")
        .expect("release signature path must accompany an artifact");
    let public_key = release_input("MAILFLOW_TEST_UPDATE_PUBLIC_KEY")
        .expect("release public key must accompany an artifact");

    let public_key = String::from_utf8(STANDARD.decode(public_key).expect("public key base64"))
        .expect("public key utf-8");
    let public_key = PublicKey::decode(&public_key).expect("minisign public key");
    let encoded_signature = fs::read_to_string(signature_path).expect("read updater signature");
    let signature = String::from_utf8(
        STANDARD
            .decode(encoded_signature.trim())
            .expect("signature base64"),
    )
    .expect("signature utf-8");
    let signature = Signature::decode(&signature).expect("minisign signature");
    let artifact = fs::read(artifact_path).expect("read updater artifact");

    public_key
        .verify(&artifact, &signature, true)
        .expect("signed updater artifact must verify");

    let mut tampered = artifact;
    let first = tampered.first_mut().expect("updater artifact is non-empty");
    *first ^= 1;
    assert!(public_key.verify(&tampered, &signature, true).is_err());
}
