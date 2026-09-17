// Browser check for the file viewer: a page an agent wrote must not run script or reach Bob's API
// as the signed-in person, while its own stylesheet, images and links still work.
//
//   BOB_COOKIE='bob_session=…' node scripts/check-file-viewer.mjs http://localhost:8080 <project>
//
// Needs Playwright (npm i -g playwright; npx playwright install chromium) and, committed to the
// project's repository and synced, site/index.html with:
//
//   <link rel="stylesheet" href="style.css">          style.css:  h1 { color: rgb(200, 0, 0); }
//   <h1 id="title">Static page</h1>
//   <img id="chart" src="chart.svg" width="80">        chart.svg:  any 80px-wide SVG
//   <a id="link" href="other.html">other</a>           other.html: anything
//   <script>document.getElementById('title').textContent = 'SCRIPT RAN'; fetch('/api/projects')</script>
import { chromium } from 'playwright'

const [origin, project] = process.argv.slice(2)
const [name, value] = (process.env.BOB_COOKIE ?? '').split(/=(.*)/s)
if (!origin || !project || !value) throw new Error('usage: BOB_COOKIE=bob_session=… node check-file-viewer.mjs <origin> <project>')

const browser = await chromium.launch()
const ctx = await browser.newContext()
await ctx.addCookies([{ name, value, url: origin, httpOnly: true, sameSite: 'Lax' }])
const page = await ctx.newPage()
const res = await page.request.post(`${origin}/api/projects/${project}/view`)
const url = origin + (await res.json()).base + 'site/index.html'

let apiCalls = 0
// Count calls made by the viewed page itself (Bob's own app also lists projects, legitimately).
page.on('request', (r) => { if (!r.isNavigationRequest() && r.frame().url().includes('/api/view/') && !r.url().includes('/api/view/')) { apiCalls++; console.log('page requested', r.url()) } })
const failures = []
const check = async (label, frame) => {
  await page.waitForTimeout(1500)
  const got = {
    title: await frame.locator('#title').textContent(),
    img: await frame.locator('#chart').evaluate((e) => e.naturalWidth),
    color: await frame.locator('#title').evaluate((e) => getComputedStyle(e).color),
  }
  if (got.title !== 'Static page') failures.push(`${label}: the page's script ran`)
  if (got.img === 0) failures.push(`${label}: the image did not load`)
  if (got.color !== 'rgb(200, 0, 0)') failures.push(`${label}: the stylesheet did not load`)
  console.log(label, got)
}

await page.goto(url)
await check('opened in a tab', page.mainFrame())
await page.goto(origin + '/')
await page.evaluate((u) => { const f = document.createElement('iframe'); f.sandbox = ''; f.src = u; document.body.appendChild(f) }, url)
await page.waitForTimeout(1000)
const frame = page.frames().find((f) => f.url() === url)
await check('in <iframe sandbox>', frame)
await frame.locator('#link').click()
await page.waitForTimeout(1000)
if (!page.frames().some((f) => f.url().endsWith('/site/other.html'))) failures.push('a relative link did not navigate within the frame')
if (apiCalls) failures.push(`the page called /api/projects ${apiCalls} times`)
await browser.close()
console.log(failures.length ? `FAIL\n${failures.join('\n')}` : 'ok')
process.exit(failures.length ? 1 : 0)
