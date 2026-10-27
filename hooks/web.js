function installWebGuard(config) {
  const key = '__slackWorkWeb';
  if (window[key]) {
    if (window[key].workspaceId !== config.workspaceId) throw new Error('Workspace configuration changed; reload Slack');
    if (!window[key].shortcutGuard) throw new Error('Reload Slack to update its UI guards');
    return window[key].status();
  }
  if (!Array.isArray(globalThis.rspackChunkGantryV2)) throw new Error('Unsupported Slack web runtime');
  let requireModule;
  globalThis.rspackChunkGantryV2.push([['slack-hours-runtime-' + Date.now()], {}, require => { requireModule = require; }]);
  if (!requireModule?.c) throw new Error('Slack module cache is unavailable');
  const selectors = Object.values(requireModule.c).map(module => module.exports).find(exports => {
    if (!exports || typeof exports !== 'object') return false;
    const names = Object.values(exports).filter(value => typeof value === 'function').map(fn => fn.name);
    return names.includes('canPlayMessageSound') && names.includes('canPlaySound');
  });
  if (!selectors) throw new Error('Unsupported Slack sound selectors');
  const soundFunction = Object.values(selectors).find(fn => typeof fn === 'function' && fn.name === 'canPlayMessageSound');
  if (!soundFunction) throw new Error('Slack sound entry point is unavailable');
  // These are the same selectors used by Slack's numbered workspace shortcuts.
  const workspaceSelectors = requireModule('8EYf');
  const workspaceStore = requireModule('PCvj');
  if (typeof workspaceSelectors?.getAllWorkspaces !== 'function' || typeof workspaceStore?.en !== 'function') {
    throw new Error('Unsupported Slack workspace shortcuts');
  }
  const guard = {
    instanceId: Date.now().toString(36) + Math.random().toString(36).slice(2),
    workspaceId: config.workspaceId, workspaceName: config.workspaceName,
    hidden: false, eventDriven: true, shortcutGuard: true, blockedShortcuts: 0, soundPatched: false, blockedSound: 0, fallback: null,
    soundFunction, selectors, originalFunctions: {},
  };
  guard.blockState = state => guard.hidden && state?.selfTeamIds?.teamId === guard.workspaceId;
  guard.wrap = (fn, blockedValue) => {
    guard.originalFunctions[fn.name] = fn;
    const async = fn.constructor.name === 'AsyncFunction';
    return function (...args) {
      if (guard.blockState(args[0])) { guard.blockedSound++; return async ? Promise.resolve(blockedValue) : blockedValue; }
      return fn.apply(this, args);
    };
  };
  const style = document.createElement('style');
  style.id = 'slack-hours-visibility';
  const sidebarSelector = '[data-qa="team_sidebar_item"][data-team="' + guard.workspaceId + '"]';
  guard.maintain = () => {
    if (!guard.hidden) return;
    if (!style.isConnected) document.head.appendChild(style);
    for (const item of document.querySelectorAll('[role="menuitem"], [role="option"]')) {
      const label = item.querySelector('[data-qa="team-icon"]')?.getAttribute('aria-label');
      if ((item.getAttribute('data-team') === guard.workspaceId || label === guard.workspaceName) && !item.hasAttribute('data-slack-hours-hidden')) item.setAttribute('data-slack-hours-hidden', '');
    }
    const items = [...document.querySelectorAll('[data-qa="team_sidebar_item"]')];
    const selected = items.find(e => e.dataset.teamActive === 'true');
    if (selected?.dataset.team && selected.dataset.team !== guard.workspaceId) guard.fallback = selected.dataset.team;
    if (selected?.dataset.team === guard.workspaceId || location.pathname.split('/')[2] === guard.workspaceId) {
      const fallback = items.find(e => e.dataset.team === guard.fallback) || items.find(e => e.dataset.team !== guard.workspaceId);
      if (fallback) { guard.fallback = fallback.dataset.team; fallback.click(); }
    }
  };
  let queued = false;
  const requestMaintenance = () => {
    if (!guard.hidden || queued) return;
    queued = true;
    queueMicrotask(() => { queued = false; guard.maintain(); });
  };
  const relevant = '[data-qa="team_sidebar_item"], [role="menuitem"], [role="option"], [data-qa="team-icon"]';
  const observer = new MutationObserver(records => {
    if (!guard.hidden) return;
    for (const record of records) {
      if (record.type === 'attributes') { requestMaintenance(); return; }
      for (const node of [...record.addedNodes, ...record.removedNodes]) {
        if (node.nodeType === 1 && (node === style || node.matches(relevant) || node.querySelector(relevant))) {
          requestMaintenance(); return;
        }
      }
    }
  });
  window.addEventListener('popstate', requestMaintenance);
  window.addEventListener('keydown', event => {
    if (!guard.hidden || !event.metaKey || event.ctrlKey || event.altKey || event.shiftKey) return;
    const code = event.keyCode;
    const index = code >= 97 && code <= 105 ? code - 97 : code >= 49 && code <= 57 ? code - 49 : -1;
    if (index < 0) return;
    const state = workspaceStore.en()?.getState();
    if (!state || workspaceSelectors.getAllWorkspaces(state)?.[index] !== guard.workspaceId) return;
    event.preventDefault();
    event.stopImmediatePropagation();
    guard.blockedShortcuts++;
  }, true);
  guard.apply = hidden => {
    observer.disconnect();
    guard.hidden = hidden;
    style.textContent = hidden ? sidebarSelector + ', [data-slack-hours-hidden] { display: none !important; }' : '';
    if (!hidden) {
      style.remove();
      for (const item of document.querySelectorAll('[data-slack-hours-hidden]')) item.removeAttribute('data-slack-hours-hidden');
    }
    guard.maintain();
    if (hidden) observer.observe(document.documentElement, { childList: true, subtree: true, attributes: true, attributeFilter: ['data-team-active'] });
    return guard.status();
  };
  guard.status = () => {
    const item = document.querySelector(sidebarSelector);
    return {
      installed: true, instanceId: guard.instanceId, workspaceId: guard.workspaceId, eventDriven: true, shortcutGuard: true, blockedShortcuts: guard.blockedShortcuts, hidden: guard.hidden, soundPatched: guard.soundPatched, blockedSound: guard.blockedSound,
      sidebarFound: !!item, sidebarVisible: item ? getComputedStyle(item).display !== 'none' : null,
      activeWorkspace: location.pathname.split('/')[2],
    };
  };
  guard.test = async () => {
    if (!guard.hidden || !guard.soundPatched) throw new Error('Self-test requires complete hidden guards');
    const state = { selfTeamIds: { teamId: guard.workspaceId } };
    const call = name => guard.patchedFunctions[name](state, {});
    return {
      messageSoundBlocked: call('canPlayMessageSound') === false,
      asyncSoundBlocked: await call('canPlaySound') === false,
      huddleSoundBlocked: call('getHuddleInviteSoundPath') === undefined,
      calendarSoundBlocked: call('getShouldMuteCalendarSounds') === true,
      otherWorkspacePasses: !guard.blockState({ selfTeamIds: { teamId: 'T_SLACK_HOURS_CONTROL' } }),
    };
  };
  window[key] = guard;
  return guard.status();
}
