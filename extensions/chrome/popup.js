// Popup: a capture on/off toggle plus a daemon-reachability line. The status
// pings the daemon through the native host so the user can tell "host not
// installed" apart from "daemon not running" — the two reasons a capture fails.

const HOST = 'io.github.k_red90.gidm'
const ENABLED_KEY = 'enabled'

const toggle = document.getElementById('toggle')
const dot = document.getElementById('dot')
const text = document.getElementById('status-text')

chrome.storage.local.get(ENABLED_KEY).then((v) => {
  toggle.checked = v[ENABLED_KEY] !== false
})

toggle.addEventListener('change', () => {
  chrome.storage.local.set({ [ENABLED_KEY]: toggle.checked })
})

function setStatus(cls, msg) {
  dot.className = 'dot ' + cls
  text.textContent = msg
}

chrome.runtime.sendNativeMessage(HOST, { ping: true }, (reply) => {
  if (chrome.runtime.lastError) {
    setStatus('bad', 'Native host not installed')
  } else if (reply && reply.ok) {
    setStatus('ok', 'Connected to gidm')
  } else {
    setStatus('bad', 'Daemon not running')
  }
})
