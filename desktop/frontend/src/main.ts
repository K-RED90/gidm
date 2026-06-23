import './lib/tokens.css'
import { mount } from 'svelte'
import App from './App.svelte'
import ProgressPopup from './components/ProgressPopup.svelte'

// A window opened with ?popup=<id> is a standalone capture progress window
// (main.go opens one per browser-captured download); everything else is the
// full manager app.
const target = document.getElementById('app')!
const popupId = new URLSearchParams(location.search).get('popup')
if (popupId) {
  mount(ProgressPopup, { target, props: { id: popupId } })
} else {
  mount(App, { target })
}
