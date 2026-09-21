// Validate emitted homepage links/assets and the two GitHub README entry points.
import { readFileSync, existsSync } from 'node:fs'
import { resolve, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'
import assert from 'node:assert/strict'

const here = dirname(fileURLToPath(import.meta.url))
const root = resolve(here, '../..')
const dist = resolve(here, 'dist')
let checked = 0
for (const name of ['index.html', 'zh/index.html']) {
  const html = readFileSync(resolve(dist, name), 'utf8')
  assert.match(html, /<main[^>]*class="product-home"/)
  assert.equal((html.match(/<h1[ >]/g) || []).length, 1, `${name}: one primary heading`)
  assert.match(html, /MCP/)
  for (const match of html.matchAll(/(?:href|src)="(\/openwarp\/[^"#?]*)/g)) {
    const local = decodeURIComponent(match[1].slice('/openwarp/'.length))
    const file = resolve(dist, local)
    assert.ok([file, `${file}.html`, resolve(file, 'index.html')].some(existsSync), `${name}: missing ${local}`)
    checked++
  }
}
for (const name of ['README.md', 'README_CN.md']) {
  const markdown = readFileSync(resolve(root, name), 'utf8')
  assert.ok(markdown.trimEnd().split('\n').length < 70, `${name}: exceeds workspace Markdown limit`)
  for (const match of markdown.matchAll(/(?:\]\(|(?:src|href)=")(\.\/[^"\s)#]+)/g)) {
    assert.ok(existsSync(resolve(root, match[1])), `${name}: missing ${match[1]}`)
    checked++
  }
}
console.log(`Site checks passed: 2 localized homepages, 2 READMEs, ${checked} local links/assets.`)
