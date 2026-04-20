from __future__ import annotations

from pydantic_settings import BaseSettings, SettingsConfigDict


class Settings(BaseSettings):
    model_config = SettingsConfigDict(env_file=".env", env_file_encoding="utf-8")

    app_name: str = "Uni API Backend"
    app_env: str = "dev"
    api_prefix: str = "/v1"
    database_url: str = "postgresql+asyncpg://uniapi:uniapi@localhost:5432/uniapi"
    session_ttl_days: int = 7
    llm_upstream_timeout_seconds: int = 300

    google_client_id: str = ""
    google_client_secret: str = ""
    google_redirect_uri: str = "http://localhost:3000/api/auth/google/callback"

    seed_demo_data: bool = False

    admin_bootstrap_token: str = ""

    resend_api_key: str = ""
    resend_from_email: str = "Uni API <onboarding@resend.dev>"
    email_verification_ttl_minutes: int = 10
    email_verification_required: bool = True

    # Billing providers
    # ZhuPay (Alipay / WeChat Pay)
    zhupay_pid: str = ""
    zhupay_private_key: str = ""
    zhupay_public_key: str = ""
    zhupay_api_base_url: str = "https://pay.lxsd.cn"
    zhupay_cny_per_credit: str = ""

    # Creem (card checkout)
    creem_api_key: str = ""
    creem_product_id: str = ""
    creem_webhook_secret: str = ""
    # Public console URL used for return URLs and front-door webhook routes.
    app_public_url: str = "http://localhost:3000"


settings = Settings()
