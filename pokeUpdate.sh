#!/bin/bash
set -e
git pull
./builder.sh
systemctl restart --user autohoster-frontend.service
