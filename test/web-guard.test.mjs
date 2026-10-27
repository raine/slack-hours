import test from 'node:test';
import assert from 'node:assert/strict';
import vm from 'node:vm';
import { installWebGuard, soundPatch } from './guards.mjs';

function setup() {
  const config = { workspaceId: 'EWORK', workspaceName: 'Work' };
  const location = { pathname: '/client/EWORK/channel' };
  const items = ['TPERSONAL', 'EWORK'].map(id => ({
    dataset: { team: id, teamActive: String(id === 'EWORK') },
    click() { for (const item of items) item.dataset.teamActive = String(item === this); location.pathname = '/client/' + id; },
  }));
  const styles = [];
  const observers = [];
  class Observer {
    constructor(callback) { this.callback = callback; observers.push(this); }
    observe() { this.active = true; }
    disconnect() { this.active = false; }
  }
  const document = {
    documentElement: {},
    head: { appendChild(style) { style.isConnected = true; if (!styles.includes(style)) styles.push(style); } },
    createElement() { return { isConnected: false, textContent: '', remove() { this.isConnected = false; } }; },
    getElementById() { return null; },
    querySelectorAll(selector) { return selector === '[data-qa="team_sidebar_item"]' ? items : []; },
    querySelector() { return items[1]; },
  };
  const listeners = {};
  const workspaceOrder = ['TPERSONAL', 'EWORK'];
  const context = vm.createContext({ config, workspaceOrder, window: { addEventListener(name, fn, capture) { listeners[name] = { fn, capture }; } }, location, document,
    getComputedStyle: item => ({ display: item.dataset.team === 'EWORK' && styles.some(s => s.isConnected && s.textContent) ? 'none' : 'block' }),
    MutationObserver: Observer, queueMicrotask,
  });
  vm.runInContext(`
    async function canPlaySound(state) { return true; }
    function canPlayMessageSound(state) { return true; }
    function getHuddleInviteSoundPath(state) { return 'huddle.mp3'; }
    function getSavedItemReminderSoundPath(state) { return 'saved.mp3'; }
    function getNewMsgSoundPath(state) { return 'message.mp3'; }
    function getShouldMuteCalendarSounds(state) { return false; }
    function shouldShowNotificationForHuddle(state) { return 0; }
    function shouldShowNotificationForChannelMessage(state) { return 0; }
    function shouldShowNotificationForDirectMessage(state) { return 0; }
    function shouldShowNotificationForDesktopNotification(state) { return 0; }
    function areGlobalDesktopNotificationsDisabled(state) { return false; }
    const selectors = { canPlaySound, canPlayMessageSound };
    const requireModule = id => id === '8EYf' ? { getAllWorkspaces: state => state.order } : id === 'PCvj' ? { en: () => ({ getState: () => ({ order: workspaceOrder }) }) } : selectors;
    requireModule.c = { sound: { exports: selectors } };
    globalThis.rspackChunkGantryV2 = [];
    globalThis.rspackChunkGantryV2.push = ([ids, factories, runtime]) => runtime(requireModule);
  `, context);
  vm.runInContext('(' + installWebGuard.toString() + ')(config)', context);
  vm.runInContext(soundPatch, context);
  return { context, g: context.window.__slackWorkWeb, location, observers, listeners, workspaceOrder };
}
test('workspace switch and style are reversible; show does not force work open', () => {
  const {g,location}=setup();g.apply(true);
  assert.equal(location.pathname,'/client/TPERSONAL');
  assert.equal(g.status().sidebarVisible,false);
  g.apply(false);
  assert.equal(g.status().sidebarVisible,true);
  assert.equal(location.pathname,'/client/TPERSONAL');
});
test('source selectors block work only and preserve async contracts',async()=>{
  const {g,context}=setup();g.apply(true);
  const work={selfTeamIds:{teamId:'EWORK'}}, personal={selfTeamIds:{teamId:'TPERSONAL'}};
  const result=g.patchedFunctions.canPlaySound(work);
  assert.equal(typeof result.then,'function');assert.equal(await result,false);
  assert.equal(await g.patchedFunctions.canPlaySound(personal),true);
  assert.equal(g.patchedFunctions.getHuddleInviteSoundPath(work),undefined);
  assert.equal(g.patchedFunctions.getHuddleInviteSoundPath(personal),'huddle.mp3');
  context.work=work;context.personal=personal;
  assert.equal(vm.runInContext('shouldShowNotificationForHuddle(work)',context),14);
  assert.equal(vm.runInContext('shouldShowNotificationForHuddle(personal)',context),0);
  g.apply(false);
  assert.equal(await g.patchedFunctions.canPlaySound(work),true);
  assert.equal(vm.runInContext('shouldShowNotificationForHuddle(work)',context),0);
});
test('web self-test and installation are idempotent',async()=>{
  const {g,context}=setup();g.apply(true);
  assert.ok(Object.values(await g.test()).every(Boolean));
  const fn=g.patchedFunctions.canPlaySound;
  vm.runInContext('('+installWebGuard.toString()+')(config)',context);
  vm.runInContext(soundPatch,context);
  assert.equal(g.patchedFunctions.canPlaySound,fn);
});

test('observer coalesces relevant changes and ignores unrelated messages', async () => {
  const { g, observers } = setup();
  g.apply(true);
  assert.equal(g.status().eventDriven, true);
  assert.equal(observers[0].active, true);
  let runs = 0;
  const original = g.maintain;
  g.maintain = () => { runs++; original(); };
  const unrelated = { nodeType: 1, matches: () => false, querySelector: () => null };
  observers[0].callback([{ type: 'childList', addedNodes: [unrelated], removedNodes: [] }]);
  await Promise.resolve();
  assert.equal(runs, 0);
  observers[0].callback([{ type: 'attributes' }]);
  observers[0].callback([{ type: 'attributes' }]);
  await Promise.resolve();
  assert.equal(runs, 1);
  g.apply(false);
  assert.equal(observers[0].active, false);
  const before = runs;
  observers[0].callback([{ type: 'attributes' }]);
  await Promise.resolve();
  assert.equal(runs, before);
});


test('number shortcuts block before selection, follow current order, and restore on show', () => {
  const { g, listeners, workspaceOrder } = setup();
  assert.equal(listeners.keydown.capture, true);
  const press = (keyCode, extra = {}) => {
    const event = { keyCode, metaKey: true, prevented: false, stopped: false,
      preventDefault() { this.prevented = true; },
      stopImmediatePropagation() { this.stopped = true; }, ...extra };
    listeners.keydown.fn(event);
    return [event.prevented, event.stopped];
  };
  g.apply(true);
  assert.deepEqual(press(50), [true, true]);
  assert.deepEqual(press(98), [true, true]);
  assert.deepEqual(press(49), [false, false]);
  assert.deepEqual(press(50, { shiftKey: true }), [false, false]);
  assert.deepEqual(press(50, { metaKey: false }), [false, false]);
  workspaceOrder.reverse();
  assert.deepEqual(press(50), [false, false]);
  assert.deepEqual(press(49), [true, true]);
  g.apply(false);
  assert.deepEqual(press(49), [false, false]);
});
