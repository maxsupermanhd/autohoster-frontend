#!/bin/bash

find -type f -name "*.go" | entr -r -s './builder.sh && ./prod'
