# AWS EC2 Production Deployment Guide

This guide details how to prepare, deploy, and operate the **Auth Service Backend** on a single AWS EC2 instance using Docker Compose.

---

## 1. Architecture on AWS EC2

```
                       [ Internet Clients / Vercel Frontend ]
                                          │
                                          ▼ (Port 8081 / or 443 via Nginx/Caddy)
                           [ EC2 Security Group ]
                                          │
                 ┌────────────────────────┴────────────────────────┐
                 │          AWS EC2 Host (Docker Engine)           │
                 │                                                 │
                 │   ┌─────────────────────────────────────────┐   │
                 │   │        Container: auth-service          │   │
                 │   │        (Non-root user: appuser)         │   │
                 │   └───────────────┬─────────────────┬───────┘   │
                 │                   │                 │           │
                 │      [ internal-net: Bridge Network (Isolated) ]│
                 │                   │                 │           │
                 │   ┌───────────────▼─────────┐ ┌─────▼───────┐   │
                 │   │   Container: postgres   │ │  Container: │   │
                 │   │   (Port 5432 Internal)  │ │    redis    │   │
                 │   └───────────────┬─────────┘ └─────┬───────┘   │
                 │                   │                 │           │
                 │         [ Docker Named Vol ]  [ Docker Vol ]    │
                 │            postgres_data        redis_data      │
                 └─────────────────────────────────────────────────┘
```

### Security Isolation Rules:
* **PostgreSQL (5432)** and **Redis (6379)** are **NOT** bound to any host ports in production (`docker-compose.prod.yml`). They communicate solely across the isolated Docker `internal-net` network.
* Only the **Auth Service** is reachable through its application port (`8081`), which should later be placed behind Caddy or Nginx terminating HTTPS on port `443`.

---

## 2. Environment Variables & Secret Generation

### Generating Production Secrets:
Generate cryptographically strong secrets on your local machine or EC2 using OpenSSL:

```bash
# 1. Generate strong PostgreSQL password
openssl rand -base64 24

# 2. Generate strong 256-bit JWT secret (minimum 32 characters)
openssl rand -base64 32
```

### EC2 Production `.env` File Setup:
On the EC2 instance, create a `.env` file in the project root:

```ini
APP_ENV=production
AUTH_PORT=8081
CORS_ALLOWED_ORIGINS=https://your-frontend-domain.vercel.app

POSTGRES_USER=postgres
POSTGRES_PASSWORD=<INSERT_GENERATED_POSTGRES_PASSWORD>
POSTGRES_DB=apigateway_auth

DATABASE_URL=postgres://postgres:<INSERT_GENERATED_POSTGRES_PASSWORD>@postgres:5432/apigateway_auth?sslmode=disable
REDIS_URL=redis://redis:6379

JWT_SECRET=<INSERT_GENERATED_JWT_SECRET_32_CHARS>
JWT_ISSUER=apigateway-auth-service
```

> **Security Note:** The `.env` file is excluded from git via `.gitignore`. Never commit real secrets to GitHub.

---

## 3. Step-by-Step EC2 Instance Setup

### Step 1: Launch EC2 Instance
* **Instance Type:** `t3.small` (Recommended: 2 vCPU, 2 GB RAM) or `t3.medium`.
* **OS:** Ubuntu 24.04 LTS or Amazon Linux 2023.
* **Storage:** 20 GB gp3 EBS volume.
* **Security Group Inbound Rules:**
  * `SSH (Port 22)`: Your IP only.
  * `Custom TCP (Port 8081)`: Anywhere (`0.0.0.0/0`) for initial testing (or restricted to your frontend/test IPs).
  * `HTTP (Port 80)` & `HTTPS (Port 443)`: Anywhere (for future TLS reverse proxy).

### Step 2: Install Docker & Docker Compose
Connect via SSH and install Docker:

```bash
# For Ubuntu
sudo apt update && sudo apt upgrade -y
sudo apt install -y docker.io docker-compose-v2 git

# Allow ec2-user/ubuntu to run docker without sudo
sudo usermod -aG docker $USER
newgrp docker
```

### Step 3: Clone Project Repository & Configure `.env`
```bash
git clone https://github.com/akhilbabu26/API_Gateway_Auth_Platform-.git apigateway
cd apigateway

# Copy template and fill production secrets
cp .env.example .env
nano .env
```

### Step 4: Launch Production Containers
```bash
# Start with production overrides (enforces secret checks, removes public DB ports)
docker compose -f docker-compose.yml -f docker-compose.prod.yml up --build -d
```

### Step 5: Verify Services & Healthchecks
```bash
# Verify container states
docker compose ps

# Check logs
docker compose logs -f auth-service
```

---

## 4. How Migrations & Seed Data Work

* **Automatic Execution:** The Auth Service binary embeds all SQL migrations using Go's `embed.FS`.
* When the `auth-service` container starts, it immediately executes `goose.Up`.
* It automatically creates:
  * `tenants` (and seeds the default platform tenant `a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11`).
  * `users` (with `token_version`).
  * `roles` and `user_roles`.
  * `refresh_tokens`.
* **Zero Manual SQL Setup:** You do not need to install `psql` or run manual scripts on EC2.

---

## 5. Safe Credential Rotation Protocol

If you ever need to rotate database credentials or JWT secrets on EC2:

1. **Rotating JWT Secret:**
   * Update `JWT_SECRET` in `.env`.
   * Run `docker compose restart auth-service`.
   * *Impact:* All active access tokens will be invalidated immediately; users will need to refresh or log in again.
2. **Rotating Database Password:**
   * Run SQL command in PostgreSQL to update the user password:
     ```bash
     docker compose exec postgres psql -U postgres -d apigateway_auth -c "ALTER USER postgres WITH PASSWORD 'new_password';"
     ```
   * Update `POSTGRES_PASSWORD` and `DATABASE_URL` in `.env`.
   * Restart containers: `docker compose restart`.
   * *Impact:* Zero downtime if planned; no data loss since data resides in `postgres_data` persistent volume.

---

## 6. Upgrading to Production HTTPS via Caddy (Recommended Next Step)

Once domain name and Vercel frontend are ready, terminate SSL on EC2 using Caddy with automatic Let's Encrypt certificates:

Create `Caddyfile` on EC2:
```caddy
api.yourdomain.com {
    reverse_proxy localhost:8081
}
```
Run Caddy:
```bash
sudo apt install -y caddy
sudo systemctl enable --now caddy
```
Then close port `8081` in your AWS Security Group, allowing only ports `80` and `443`!
