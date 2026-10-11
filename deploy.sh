#!/usr/bin/env bash
set -e

# Antigravity English Teacher & Voice Companion
# Google Cloud Run Deployment Script

echo "=========================================================="
echo " Deploying English Teacher WebApp to Google Cloud Run"
echo "=========================================================="

# Check for GEMINI_API_KEY
if [ -z "$GEMINI_API_KEY" ]; then
  echo "Warning: GEMINI_API_KEY environment variable is not set."
  echo "You can provide it during deploy or set it later in Cloud Run console."
  read -p "Enter Gemini API Key (or press Enter to skip): " INPUT_KEY
  if [ -n "$INPUT_KEY" ]; then
    GEMINI_API_KEY="$INPUT_KEY"
  fi
fi

SERVICE_NAME="english-teacher-app"
REGION="europe-west1"
TIMEOUT="3600" # Support up to 60-minute long-lived WebSocket voice sessions

echo "Deploying service '$SERVICE_NAME' to region '$REGION'..."

gcloud run deploy "$SERVICE_NAME" \
  --source . \
  --platform managed \
  --region "$REGION" \
  --allow-unauthenticated \
  --timeout "$TIMEOUT" \
  --max-instances 1 \
  --memory 512Mi \
  --cpu 1 \
  --set-env-vars GEMINI_API_KEY="${GEMINI_API_KEY}",GEMINI_LIVE_MODEL="auto",DB_PATH="/data/app.db",ADMIN_EMAILS="attila.private@gmail.com,attila.hitb@gmail.com",SESSION_SECRET="antigravity-english-teacher-session-secret-attila-2026"

echo "=========================================================="
echo " Deployment Complete! Service URL:"
gcloud run services describe "$SERVICE_NAME" --platform managed --region "$REGION" --format 'value(status.url)'
echo "=========================================================="
