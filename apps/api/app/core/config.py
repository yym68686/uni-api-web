from __future__ import annotations

from pydantic_settings import BaseSettings, SettingsConfigDict


class Settings(BaseSettings):
    model_config = SettingsConfigDict(env_file=".env", env_file_encoding="utf-8")

    app_name: str = "Uni API Backend"
    app_env: str = "dev"
    api_prefix: str = "/v1"
    database_url: str = "postgresql+asyncpg://uniapi:uniapi@localhost:5432/uniapi"
    session_ttl_days: int = 7


settings = Settings()
