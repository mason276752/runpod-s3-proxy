# runpod_proxy

A local HTTP proxy that works around compatibility issues in RunPod's S3 API, allowing standard S3 client tools to access RunPod storage.

## Problem

RunPod's S3 API has several non-standard behaviours that break most S3 browser and client tools:

- Date headers use `UTC` instead of the RFC-compliant `GMT` timezone string
- Responses include non-standard headers that cause S3 clients to fail or misbehave

As a result, tools that work correctly with AWS S3 or other S3-compatible providers will often fail when pointed directly at RunPod.

## Solution

This proxy sits between your S3 tool and RunPod. It:

1. Accepts incoming S3 requests from any client
2. Re-signs each request with AWS Signature Version 4 using your RunPod credentials
3. Fixes up response headers (e.g. replaces `UTC` with `GMT` in date fields) before returning them to the client

Point your S3 tool at `http://localhost:9000` instead of RunPod directly, and it will work as expected.

## Configuration

Credentials are read from a `.env` file in the working directory, or from environment variables.

Copy `.env.example` to `.env` and fill in your details:

```
cp .env.example .env
```

`.env` format:

```
RUNPOD_ACCESS_KEY=user_xxxxxxxxxxxxxxxxxx
RUNPOD_SECRET_KEY=rps_xxxxxxxxxxxxxxxxxx
RUNPOD_REGION=eu-ro-1
```

- `RUNPOD_ACCESS_KEY` / `RUNPOD_SECRET_KEY`: found in the RunPod dashboard under **Storage → API Keys**
- `RUNPOD_REGION`: the region code of your RunPod storage (e.g. `eu-ro-1`), used to build the upstream API endpoint

The `.env` file is optional if the variables are already set in your environment.

## Build & Run

```bash
go build -o runpod_proxy runpod_proxy.go
./runpod_proxy            # listens on :9000
./runpod_proxy :8080      # custom port
```

Then configure your S3 tool to use `http://localhost:9000` as the endpoint.
