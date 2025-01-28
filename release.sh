#!/bin/bash
TAG=0.9.2

git tag -d  $TAG
git push origin --delete $TAG
gh release delete $TAG -y

git add -am "$TAG"

# Extract the first two parts and the third number from the tag
FIRST_TWO_PARTS=$(echo $TAG | awk -F. '{print $1"."$2}')
THIRD_NUMBER=$(echo $TAG | awk -F. '{print $3}' | awk -F- '{printf "%02d", $1}')

# Determine the suffix based on the tag
if [[ $TAG == *"-alpha"* ]]; then
  MAPPED_RELEASE="1"
  PRE_RELEASE=$(echo $TAG | awk -F- '{print $2}' | sed 's/alpha//')
elif [[ $TAG == *"-beta"* ]]; then
  MAPPED_RELEASE="2"
  PRE_RELEASE=$(echo $TAG | awk -F- '{print $2}' | sed 's/beta//')
elif [[ $TAG == *"-rc"* ]]; then
  MAPPED_RELEASE="3"
  PRE_RELEASE=$(echo $TAG | awk -F- '{print $2}' | sed 's/rc//')
else
  MAPPED_RELEASE="4"
  PRE_RELEASE="00"
fi

# Set the app version
APP_VERSION="${FIRST_TWO_PARTS}.${THIRD_NUMBER}${MAPPED_RELEASE}${PRE_RELEASE}"
echo "App version: $APP_VERSION THIRD_NUMBER: $THIRD_NUMBER MAPPED_RELEASE: $MAPPED_RELEASE PRE_RELEASE: $PRE_RELEASE"

# Package the app for arm64
fyne-cross linux --arch arm64 --app-id app.owlcms.replays --app-version $APP_VERSION ./cmd/replays

# Package the app for Windows
fyne-cross windows --app-id app.owlcms.replays --app-version $APP_VERSION ./cmd/replays

# Determine if the release should be marked as a prerelease
if [[ $TAG == *"-"* ]]; then
  PRERELEASE_FLAG="--prerelease"
else
  PRERELEASE_FLAG=""
fi

# Create a release using the tag and ReleaseNotes.md
gh release create $TAG -F ReleaseNotes.md $PRERELEASE_FLAG --title "owlcms jury replays $TAG"

# Upload the executables
gh release upload $TAG fyne-cross/bin/linux-arm64/replays fyne-cross/bin/windows-amd64/replays.exe
