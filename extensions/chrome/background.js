// gidm capture service worker. It intercepts browser-initiated downloads, grabs
// the resolved URL plus the page's referrer and cookies, and hands them to the
// gidm daemon through the native-messaging host. Only on a confirmed hand-off is
// the browser's own download cancelled — so an unreachable daemon never loses a
// download (the browser just downloads it normally).

const HOST = 'io.github.k_red90.gidm'
const ENABLED_KEY = 'enabled'

chrome.runtime.onInstalled.addListener(async () => {
  const v = await chrome.storage.local.get(ENABLED_KEY)
  if (v[ENABLED_KEY] === undefined) await chrome.storage.local.set({ [ENABLED_KEY]: true })
  chrome.contextMenus.create({
    id: 'gidm-download-link',
    title: 'Download with gidm',
    contexts: ['link'],
  })
})

async function isEnabled() {
  const v = await chrome.storage.local.get(ENABLED_KEY)
  return v[ENABLED_KEY] !== false
}

function isHttp(url) {
  return typeof url === 'string' && (url.startsWith('http://') || url.startsWith('https://'))
}

// cookieHeader serializes the browser's cookies for url into a Cookie header so
// session/auth-gated downloads still work once gidm refetches them.
async function cookieHeader(url) {
  try {
    const cookies = await chrome.cookies.getAll({ url })
    return cookies.map((c) => `${c.name}=${c.value}`).join('; ')
  } catch {
    return ''
  }
}

// send does one native-messaging round-trip. It rejects on transport failure
// (host not installed / not reachable) so callers can keep the browser download.
function send(msg) {
  return new Promise((resolve, reject) => {
    chrome.runtime.sendNativeMessage(HOST, msg, (reply) => {
      if (chrome.runtime.lastError) return reject(new Error(chrome.runtime.lastError.message))
      resolve(reply || { ok: false, error: 'empty reply' })
    })
  })
}

// flashBadge shows a brief ✓/! on the toolbar icon as capture feedback.
async function flashBadge(text, color) {
  await chrome.action.setBadgeBackgroundColor({ color })
  await chrome.action.setBadgeText({ text })
  setTimeout(() => chrome.action.setBadgeText({ text: '' }), 4000)
}

async function capture(url, referrer) {
  const cookie = await cookieHeader(url)
  return send({ url, referrer: referrer || '', cookie })
}

// Auto-intercept: hand off FIRST, cancel SECOND. On any failure leave the browser
// download alone — never cancel into a black hole.
chrome.downloads.onCreated.addListener(async (item) => {
  if (!(await isEnabled())) return
  const url = item.finalUrl || item.url
  if (!isHttp(url)) return
  try {
    const reply = await capture(url, item.referrer)
    if (reply && reply.ok) {
      await chrome.downloads.cancel(item.id)
      await chrome.downloads.erase({ id: item.id })
      await flashBadge('✓', '#16a34a')
    } else {
      await flashBadge('!', '#dc2626') // daemon rejected; browser keeps the download
    }
  } catch {
    await flashBadge('!', '#dc2626') // host unreachable; browser keeps the download
  }
})

chrome.contextMenus.onClicked.addListener(async (info) => {
  if (info.menuItemId !== 'gidm-download-link' || !isHttp(info.linkUrl)) return
  try {
    const reply = await capture(info.linkUrl, info.pageUrl)
    await flashBadge(reply && reply.ok ? '✓' : '!', reply && reply.ok ? '#16a34a' : '#dc2626')
  } catch {
    await flashBadge('!', '#dc2626')
  }
})
