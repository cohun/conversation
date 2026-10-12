#!/usr/bin/env bash
set -e

# Antigravity English Teacher & Voice Companion
# Google Cloud Run Deployment Script

echo "=========================================================="
echo " Deploying English Teacher WebApp to Google Cloud Run"
echo "=========================================================="

SERVICE_NAME="english-teacher-app"
REGION="europe-west1"
TIMEOUT="3600" # Support up to 60-minute long-lived WebSocket voice sessions

echo "Deploying service '$SERVICE_NAME' to region '$REGION'..."

ENV_VARS="GEMINI_LIVE_MODEL=auto,DB_PATH=/data/app.db,ADMIN_EMAILS=attila.private@gmail.com;attila.hitb@gmail.com,SESSION_SECRET=antigravity-english-teacher-session-secret-attila-2026,APP_URL=https://english-teacher-app-916343130758.europe-west1.run.app"

# If GEMINI_API_KEY is explicitly set in shell, update it; otherwise preserve existing Cloud Run secret
if [ -n "$GEMINI_API_KEY" ]; then
  ENV_VARS="GEMINI_API_KEY=${GEMINI_API_KEY},${ENV_VARS}"
fi

gcloud run deploy "$SERVICE_NAME" \
  --source . \
  --platform managed \
  --region "$REGION" \
  --allow-unauthenticated \
  --timeout "$TIMEOUT" \
  --max-instances 1 \
  --memory 512Mi \
  --cpu 1 \
  --update-env-vars "$ENV_VARS"

echo "=========================================================="
echo " Deployment Complete! Service URL:"
gcloud run services describe "$SERVICE_NAME" --platform managed --region "$REGION" --format 'value(status.url)'
echo "=========================================================="
