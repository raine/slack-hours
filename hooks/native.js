function installNativeGuard(store, config, restore) {
  const key = '__slackWorkNative';
  if (window[key]) {
    if (window[key].workspaceId !== config.workspaceId) throw new Error('Workspace configuration changed; reload Slack');
    return window[key].status();
  }
  const ipc = this.ipcRenderer;
  const originalInvoke = ipc.invoke;
  const originalSend = ipc.send;
  const state = store.getState();
  let workspaces = state.workspaces || {};
  const recovered = !workspaces[config.workspaceId] && restore?.workspace?.id === config.workspaceId;
  if (recovered) {
    const entries = Object.entries(workspaces).sort((a, b) => a[1].order - b[1].order);
    const position = Math.max(0, Math.min(restore.workspace.order ?? entries.length, entries.length));
    entries.splice(position, 0, [config.workspaceId, restore.workspace]);
    workspaces = Object.fromEntries(entries.map(([id, workspace], order) => [id, { ...workspace, order }]));
  }
  if (!workspaces[config.workspaceId]) throw new Error('Configured workspace is absent');
  const guard = {
    workspaceId: config.workspaceId, hidden: false, blocked: 0,
    workspaces,
    unreads: recovered ? restore.unreads : state.webapp?.teams?.[config.workspaceId]?.unreads,
  };
  guard.matches = payload => {
    if (payload?.teamId === guard.workspaceId) return true;
    try { return new URL(payload?.launchUri).searchParams.get('team') === guard.workspaceId; }
    catch { return false; }
  };
  guard.filteredWorkspaces = () => {
    if (!guard.hidden) return guard.workspaces;
    return Object.fromEntries(Object.entries(guard.workspaces).filter(([id]) => id !== guard.workspaceId)
      .sort((a, b) => a[1].order - b[1].order).map(([id, workspace], order) => [id, { ...workspace, order }]));
  };
  guard.filterUpdate = payload => {
    const team = payload?.teams?.[guard.workspaceId];
    if (!team || !Object.hasOwn(team, 'unreads')) return payload;
    guard.unreads = team.unreads;
    if (!guard.hidden) return payload;
    return { ...payload, teams: { ...payload.teams, [guard.workspaceId]: { ...team,
      unreads: { ...team.unreads, showBullet: false, unreadHighlights: 0, unreads: 0 },
    } } };
  };
  ipc.invoke = function (channel, ...args) {
    if (guard.hidden && channel === 'SLACK_NOTIFY' && guard.matches(args[0])) {
      guard.blocked++;
      return Promise.resolve(undefined);
    }
    return originalInvoke.call(this, channel, ...args);
  };
  ipc.send = function (channel, ...args) {
    if (channel === 'SYNC_WORKSPACES' && args[0] && typeof args[0] === 'object') {
      guard.workspaces = args[0];
      args[0] = guard.filteredWorkspaces();
    } else if (channel === 'REDUX_UPDATE_FROM_WEBAPP') {
      args[0] = guard.filterUpdate(args[0]);
    }
    return originalSend.call(this, channel, ...args);
  };
  guard.apply = hidden => {
    guard.hidden = hidden;
    originalSend.call(ipc, 'SYNC_WORKSPACES', guard.filteredWorkspaces());
    if (guard.unreads) {
      const unreads = hidden ? { ...guard.unreads, showBullet: false, unreadHighlights: 0, unreads: 0 } : guard.unreads;
      originalSend.call(ipc, 'REDUX_UPDATE_FROM_WEBAPP', { teams: { [guard.workspaceId]: { unreads } } });
    }
    return guard.status();
  };
  guard.status = () => ({
    installed: true, workspaceId: guard.workspaceId, hidden: guard.hidden, blocked: guard.blocked,
    workspaceKnown: !!guard.workspaces[guard.workspaceId],
    nativeWorkspaceVisible: !!store.getState().workspaces[guard.workspaceId],
    notificationHookIntact: ipc.invoke === guard.invokeHook,
    workspaceHookIntact: ipc.send === guard.sendHook,
  });
  guard.test = async () => {
    if (!guard.hidden) throw new Error('Self-test requires hidden mode');
    const before = guard.blocked;
    await ipc.invoke('SLACK_NOTIFY', { teamId: guard.workspaceId, id: 'slack-hours-local-self-test', title: 'Local filter test', silent: true });
    return { notificationIntercepted: guard.blocked === before + 1, otherWorkspacePasses: !guard.matches({ teamId: 'T_SLACK_HOURS_CONTROL' }) };
  };
  guard.invokeHook = ipc.invoke;
  guard.sendHook = ipc.send;
  window[key] = guard;
  return guard.status();
}
