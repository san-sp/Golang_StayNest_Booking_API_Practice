# StayNest — Hotel Booking API

A production-oriented hotel booking backend built with Go, Gin, and PostgreSQL. StayNest provides customer authentication, hotel and room discovery, multi-room bookings, inventory management, payment processing, and background workers.

The project focuses on backend engineering challenges such as database transactions, concurrent inventory management, payment idempotency, webhook verification, and reliable background processing.

## Technology Stack

| Area | Technology |
|---|---|
| Language | Go |
| HTTP framework | Gin |
| Database | PostgreSQL 17 |
| Database driver | pgx/v5 |
| Authentication | JWT |
| Containerization | Docker, Docker Compose |
| Payments | Razorpay |
| Testing | Go testing package |
| CI/CD | GitHub Actions |

## Features

### Authentication and Customers
- Customer registration and login
- JWT-based authentication
- Protected customer endpoints
- Customer profile management
- Password change
- Account deactivation

### Hotels and Rooms
- Hotel creation and management
- Room type management
- Public hotel and room discovery
- Hotel search
- Room inventory management

### Booking Management
- Multi-room bookings
- Booking creation, retrieval, modification, and cancellation
- Date-based room inventory management
- Transactional inventory updates
- Protection against concurrent overbooking
- Booking expiration through a background worker

### Payment Processing
- Razorpay payment initiation
- Payment verification
- Webhook signature verification using the raw request body
- Idempotent payment and webhook processing
- Handling different payment and webhook arrival orders
- Refund processing through a background worker

### Reliability and Security
- Strict JSON request validation
- Request body size limits
- Rejection of unknown JSON fields and trailing JSON values
- Login IP rate limiting
- JWT-protected routes
- Health and readiness endpoints
- Graceful server shutdown
- Background worker lifecycle management

## Engineering Highlights

### 1. Concurrent Inventory Management

Booking requests can arrive simultaneously for the same room and date.

StayNest uses PostgreSQL transactions and row-level locking to protect inventory updates and prevent multiple customers from booking the same available inventory.

### 2. Transactional Booking Operations

Operations that affect bookings and inventory are performed transactionally.

This helps maintain consistency when booking creation, modification, or cancellation involves multiple database changes.

### 3. Payment Idempotency

Payment operations and webhook processing must tolerate duplicate requests.

StayNest implements idempotency protections so that repeated payment-related events do not unintentionally apply the same operation more than once.

### 4. Webhook Verification

Razorpay webhook signatures are verified against the raw request body before processing the event.

This helps ensure that incoming webhook requests are authentic.

### 5. Background Workers

StayNest uses background workers for time-sensitive operations, including:

- Expiring eligible bookings
- Processing refunds
- Retrying and reconciling refund operations

The refund worker uses leases and retry handling to support reliable processing.

### 6. Graceful Shutdown

The application handles graceful shutdown by stopping background workers before closing the database connection pool.

This reduces the risk of workers attempting database operations after the database pool has been closed.

## Architecture Overview

```mermaid
flowchart TD
    Client[Client Application]
    API[Go API - Gin]
    Auth[Authentication and Middleware]
    Booking[Booking Service]
    Payment[Payment Service]
    DB[(PostgreSQL)]
    Razorpay[Razorpay]
    Expiry[Booking Expiry Worker]
    Refund[Refund Worker]

    Client --> API
    API --> Auth
    Auth --> Booking
    Auth --> Payment
    Booking --> DB
    Payment --> DB
    Payment <--> Razorpay
    Expiry --> DB
    Refund --> DB
    Refund <--> Razorpay
```

## API Overview

Base URL:

```text
http://localhost:8081
```

### Health

| Method | Endpoint | Purpose |
|---|---|---|
| GET | `/health` | Application health |
| GET | `/ready` | Application readiness |

### Authentication

| Method | Endpoint | Purpose |
|---|---|---|
| POST | `/api/v1/auth/register` | Register a customer |
| POST | `/api/v1/auth/login` | Customer login |
| POST | `/api/v1/auth/change-password` | Change password |

### Hotels and Rooms

| Method | Endpoint | Purpose |
|---|---|---|
| GET | `/api/v1/hotels` | List hotels |
| GET | `/api/v1/hotels/search` | Search hotels |
| GET | `/api/v1/hotels/:id` | Get hotel details |
| GET | `/api/v1/hotels/:id/rooms` | List hotel rooms |
| GET | `/api/v1/rooms/:id` | Get room details |
| GET | `/api/v1/rooms/:id/inventory` | View room inventory |

### Customer

| Method | Endpoint | Purpose |
|---|---|---|
| GET | `/api/v1/customers/me` | Get customer profile |
| PATCH | `/api/v1/customers/me` | Update customer profile |
| DELETE | `/api/v1/customers/me` | Deactivate customer account |

### Bookings

| Method | Endpoint | Purpose |
|---|---|---|
| GET | `/api/v1/bookings` | List customer bookings |
| GET | `/api/v1/bookings/:id` | Get booking details |
| POST | `/api/v1/bookings` | Create booking |
| PATCH | `/api/v1/bookings/:id` | Modify booking |
| DELETE | `/api/v1/bookings/:id` | Cancel booking |

### Payments

| Method | Endpoint | Purpose |
|---|---|---|
| POST | `/api/v1/bookings/:id/payments` | Initiate payment |
| POST | `/api/v1/payments/verify` | Verify payment |
| POST | `/api/v1/webhooks/razorpay` | Receive Razorpay webhooks |

### Admin

| Method | Endpoint | Purpose |
|---|---|---|
| POST | `/api/v1/admin/hotels` | Create hotel |
| PATCH | `/api/v1/admin/hotels/:id` | Update hotel |
| POST | `/api/v1/admin/hotels/:id/rooms` | Create room |
| PATCH | `/api/v1/admin/rooms/:id` | Update room |
| PUT | `/api/v1/admin/rooms/:id/inventory/:date` | Update inventory |

Protected endpoints require authentication. Admin endpoints require appropriate administrative authorization.

## Getting Started

### Prerequisites

- Go 1.27.1
- Docker
- Docker Compose
- Git

### 1. Clone the repository

```bash
git clone https://github.com/san-sp/Golang_StayNest_Booking_API_Practice.git

cd Golang_StayNest_Booking_API_Practice
```

### 2. Configure environment variables

Copy the example environment file:

```bash
cp .env.example .env
```

Configure the required values in `.env`:

```env
APP_ENV=development

POSTGRES_DB=staynest
POSTGRES_USER=staynest
POSTGRES_PASSWORD=your_strong_password

DATABASE_URL=postgres://staynest:your_strong_password@localhost:5437/staynest?sslmode=disable

DATABASE_URL_DOCKER=postgres://staynest:your_strong_password@postgres:5432/staynest?sslmode=disable

JWT_SECRET=your_generated_secret

RAZORPAY_KEY_ID=
RAZORPAY_KEY_SECRET=
RAZORPAY_WEBHOOK_SECRET=
```

Generate a strong JWT secret:

```bash
openssl rand -base64 32
```

Keep your actual `.env` file private. Never commit production credentials or secrets to Git.

### 3. Start the application

```bash
docker compose up --build -d
```

Check running services:

```bash
docker compose ps
```

### 4. Verify the application

```bash
curl http://localhost:8081/health
```

```bash
curl http://localhost:8081/ready
```

Expected responses:

```json
{"status":"ok"}
```

```json
{"status":"ready"}
```

## Database Migrations

Database migration files are maintained in the `migrations/` directory.

Apply the migrations in order using the configured database connection.

For example, using PostgreSQL's command-line client:

```bash
for file in migrations/*.up.sql; do
    echo "Applying $file"
    psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f "$file" || exit 1
done
```

## Testing

Run all Go tests:

```bash
go test ./... -count=1
```

Run the race detector:

```bash
go test -race ./... -count=1
```

Run static analysis:

```bash
go vet ./...
```

Build all packages:

```bash
go build ./...
```

## Continuous Integration

StayNest uses GitHub Actions to automatically validate changes pushed to the `main` branch and pull requests targeting `main`.

The CI pipeline includes:

1. PostgreSQL service setup
2. Database migrations
3. Go formatting verification
4. `go vet`
5. Race-enabled tests
6. Go build verification

A successful CI run provides automated evidence that the repository passes these checks.

## Project Goals

StayNest was developed as a backend engineering project to demonstrate practical Go development and the ability to handle real-world backend challenges.

The primary engineering goals are:

- Building maintainable REST APIs
- Designing relational database schemas
- Managing concurrent database operations
- Maintaining transactional consistency
- Implementing secure authentication
- Integrating external payment services
- Designing idempotent operations
- Building reliable background workers
- Writing automated tests
- Using Docker and CI for repeatable development workflows

## Author

**Santhosh SP**

GitHub: [san-sp](https://github.com/san-sp)

Project: [Golang StayNest Booking API](https://github.com/san-sp/Golang_StayNest_Booking_API_Practice)
