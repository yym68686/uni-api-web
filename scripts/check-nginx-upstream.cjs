const { test } = require('node:test');
const assert = require('node:assert/strict');
const { spawnSync } = require('node:child_process');
const { resolve } = require('node:path');

function parse(url) {
  return spawnSync('sh', ['-c', '. "$1"; printf "%s\\n%s\\n%s\\n" "$ANALYTICS_UPSTREAM_SCHEME" "$ANALYTICS_UPSTREAM_ADDRESS" "$ANALYTICS_UPSTREAM_PATH"', 'test', resolve(__dirname, '15-analytics-upstream.envsh')], {
    env: { ...process.env, ANALYTICS_UPSTREAM: url }, encoding: 'utf8',
  });
}

test('preserves configured HTTP, HTTPS, IPv6, ports and base paths', () => {
  for (const [url, expected] of [
    ['http://analytics-api:8080', ['http', 'analytics-api:8080', '']],
    ['https://analytics.example:8443/base', ['https', 'analytics.example:8443', '/base']],
    ['https://analytics.example', ['https', 'analytics.example', '']],
    ['http://[::1]:8080/a-b', ['http', '[::1]:8080', '/a-b']],
  ]) {
    const result = parse(url);
    assert.equal(result.status, 0, result.stderr);
    assert.deepEqual(result.stdout.trimEnd().split('\n'), expected.at(-1) === '' ? expected.slice(0, -1) : expected);
  }
});

test('rejects invalid upstream configuration instead of injecting nginx directives', () => {
  for (const url of ['ftp://example', 'http://', 'http://user:password@example', 'http://example;server', 'http://example/a?b', 'http://example/a\n}']) {
    assert.notEqual(parse(url).status, 0, url);
  }
});
