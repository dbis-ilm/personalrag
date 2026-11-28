#!/usr/bin/env bash
# Sets the app version everywhere it is written down, for a release build.
# The release workflow runs it on its checkout; the changes are not
# committed. Usage: build/set-version.sh 1.2.3 [root]
set -euo pipefail

version="${1:-}"
root="${2:-.}"
if ! [[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.]+)?$ ]]; then
  echo "usage: $0 MAJOR.MINOR.PATCH[-PRERELEASE] [root]" >&2
  exit 2
fi
# Windows file versions and macOS bundle versions are numeric: drop a
# prerelease suffix there.
numeric="${version%%-*}"

edit() { # file, sed expression; fails if the file does not change
  local file="$root/$1"
  local before
  before="$(cat "$file")"
  sed -E -i.bak "$2" "$file" && rm -f "$file.bak"
  if [[ "$(cat "$file")" == "$before" ]] && ! grep -q -- "$3" "$file"; then
    echo "set-version: no version found in $1" >&2
    exit 1
  fi
}

edit main.go 's/^var version = ".*"/var version = "'"$version"'"/' "var version = \"$version\""
edit build/config.yml 's/^(  version: )"[^"]*"/\1"'"$version"'"/' "version: \"$version\""
edit build/darwin/Info.plist '/<key>CFBundle(Short)?Version(String)?<\/key>/{n;s#<string>[^<]*</string>#<string>'"$numeric"'</string>#;}' "<string>$numeric</string>"
edit build/windows/info.json 's/"((file|product)_version|FileVersion|ProductVersion)": "[^"]*"/"\1": "'"$numeric"'"/g' "\"$numeric\""
edit build/windows/nsis/wails_tools.nsh 's/(!define INFO_PRODUCTVERSION )"[^"]*"/\1"'"$numeric"'"/' "INFO_PRODUCTVERSION \"$numeric\""
edit build/linux/nfpm/nfpm.yaml 's/^version: "[^"]*"/version: "'"$version"'"/' "version: \"$version\""
echo "version set to $version"
