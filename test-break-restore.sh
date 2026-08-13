#!/bin/bash
# Break-and-restore verification for S4-3.
# Each pinned sentence and rule must fail when broken.

set -e

echo "=== Break-and-restore verification for Part A ==="
echo ""

cd ui

# Test 1: Break a pinned sentence
echo "Test 1: Changing a pinned sentence should fail the test"
cp src/views/ConnectionComparisonDisplay.tsx src/views/ConnectionComparisonDisplay.tsx.backup
sed -i '' 's/This connection matches what Sharko intends./This connection is synced./g' src/views/ConnectionComparisonDisplay.tsx

echo "Running test (should FAIL)..."
if npx vitest run src/views/__tests__/ConnectionComparisonDisplay.test.tsx --reporter=tap 2>&1 | grep -q "not ok"; then
  echo "✓ Test FAILED as expected when sentence was changed"
else
  echo "✗ Test PASSED when it should have FAILED — the pinning test is broken"
fi

# Restore
mv src/views/ConnectionComparisonDisplay.tsx.backup src/views/ConnectionComparisonDisplay.tsx

echo ""
echo "Test 2: Dropping half a pinned sentence should fail the test"
cp src/views/ConnectionComparisonDisplay.tsx src/views/ConnectionComparisonDisplay.tsx.backup
sed -i '' 's/This connection matches what Sharko intends./This connection matches./g' src/views/ConnectionComparisonDisplay.tsx

echo "Running test (should FAIL)..."
if npx vitest run src/views/__tests__/ConnectionComparisonDisplay.test.tsx --reporter=tap 2>&1 | grep -q "not ok"; then
  echo "✓ Test FAILED as expected when sentence was truncated"
else
  echo "✗ Test PASSED when it should have FAILED — the pinning test is broken"
fi

# Restore
mv src/views/ConnectionComparisonDisplay.tsx.backup src/views/ConnectionComparisonDisplay.tsx

echo ""
echo "Test 3: Making 'limited' claim 'fully synced' should fail"
cp src/views/ConnectionComparisonDisplay.tsx src/views/ConnectionComparisonDisplay.tsx.backup
sed -i '' 's/Sharko checked part of this connection./This connection is fully synced./g' src/views/ConnectionComparisonDisplay.tsx

echo "Running test (should FAIL)..."
if npx vitest run src/views/__tests__/ConnectionComparisonDisplay.test.tsx --reporter=tap 2>&1 | grep -q "not ok"; then
  echo "✓ Test FAILED as expected when limited claimed fully synced"
else
  echo "✗ Test PASSED when it should have FAILED — the banned-phrase test is broken"
fi

# Restore
mv src/views/ConnectionComparisonDisplay.tsx.backup src/views/ConnectionComparisonDisplay.tsx

echo ""
echo "Test 4: All tests should PASS with correct code"
if npx vitest run src/views/__tests__/ConnectionComparisonDisplay.test.tsx --reporter=tap 2>&1 | grep -q "ok 13"; then
  echo "✓ All tests PASS with correct code"
else
  echo "✗ Tests are FAILING with correct code — something is wrong"
fi

echo ""
echo "=== Break-and-restore verification complete ==="
