export type AuthConfig = {
  audience: string;
  databaseUrl: string;
  secret: string;
  baseUrl: string;
  bootstrapToken: string;
  port: number;
  trustedOrigins: string[];
};

import { readFileSync } from "node:fs";

function required(name: string): string {
  const value = process.env[name];
  if (!value) throw new Error(`${name} is required`);
  return value;
}

function secret(name: string): string {
  const file = process.env[`${name}_FILE`];
  if (file) return readFileSync(file, "utf8").trim();
  return required(name);
}

function databaseUrl(): string {
  if (process.env.DATABASE_URL) return process.env.DATABASE_URL;
  const password = secret("POSTGRES_PASSWORD");
  const host = process.env.POSTGRES_HOST ?? "postgres";
  const port = process.env.POSTGRES_PORT ?? "5432";
  const user = process.env.POSTGRES_USER ?? "mailflow";
  const database = process.env.POSTGRES_DB ?? "mailflow";
  return `postgres://${encodeURIComponent(user)}:${encodeURIComponent(password)}@${host}:${port}/${database}`;
}

export function loadConfig(): AuthConfig {
  return {
    audience: process.env.MAILFLOW_AUTH_AUDIENCE ?? "mailflow-api",
    databaseUrl: databaseUrl(),
    secret: secret("BETTER_AUTH_SECRET"),
    baseUrl: process.env.BETTER_AUTH_URL ?? "http://localhost:3001",
    bootstrapToken: secret("MAILFLOW_BOOTSTRAP_TOKEN"),
    port: Number.parseInt(process.env.PORT ?? "3001", 10),
    trustedOrigins: (process.env.TRUSTED_ORIGINS ?? "http://localhost:4310")
      .split(",")
      .map((origin) => origin.trim())
      .filter(Boolean),
  };
}
