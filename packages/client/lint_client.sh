#!/bin/bash

set -e # Exit on any errors

# Get the directory of this script
# https://stackoverflow.com/questions/59895/getting-the-source-directory-of-a-bash-script-from-within
DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" >/dev/null 2>&1 && pwd )"

SECONDS=0

cd "$DIR"

# Step 1 - Use Prettier to check formatting
npx prettier --check "src/**/*.ts"

# Step 2 - Use ESLint to lint the TypeScript
# Since all ESLint errors are set to warnings,
# we set max warnings to 0 so that warnings will fail in CI
npx eslint --max-warnings 0 src

# Step 3 - Check for unused imports
# The "--error" flag makes it return an error code of 1 if unused exports are found
npx ts-prune --error

echo "Successfully linted in $SECONDS seconds."
