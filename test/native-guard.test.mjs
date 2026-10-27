import test from 'node:test';
import assert from 'node:assert/strict';
import vm from 'node:vm';
import { installNativeGuard } from './guards.mjs';
function setup() {
  const work = 'EWORK', personal = 'TPERSONAL';
  const originalUnread = {showBullet:true,unreadHighlights:3,unreads:5};
  const state = {workspaces:{[work]:{id:work,order:1},[personal]:{id:personal,order:0}},webapp:{teams:{[work]:{unreads:originalUnread}}}};
  const sent=[], invoked=[];
  const ipc = {
    invoke(...args){invoked.push(args);return Promise.resolve('delivered')},
    send(channel,payload){
      sent.push([channel,payload]);
      if(channel==='SYNC_WORKSPACES') state.workspaces=payload;
      if(channel==='REDUX_UPDATE_FROM_WEBAPP') for(const [id,team] of Object.entries(payload.teams||{})) state.webapp.teams[id]={...state.webapp.teams[id],...team};
    },
  };
  const originalInvoke = ipc.invoke, originalSend = ipc.send;
  const context = {window:{},URL,electron:{ipcRenderer:ipc},store:{getState:()=>state},config:{workspaceId:work}};
  vm.runInNewContext('('+installNativeGuard.toString()+').call(electron,store,config)', context);
  return {g:context.window.__slackWorkNative,ipc,state,sent,invoked,work,personal,originalUnread,context,originalInvoke,originalSend};
}
test('hide filters only workspace metadata and unread counts; show restores', () => {
  const {g,state,work,personal,originalUnread}=setup();
  g.apply(true);
  assert.deepEqual(Object.keys(state.workspaces),[personal]);
  assert.equal(state.webapp.teams[work].unreads.unreadHighlights,0);
  assert.equal(g.status().nativeWorkspaceVisible,false);
  g.apply(false);
  assert.equal(state.workspaces[work].id,work);
  assert.deepEqual(state.webapp.teams[work].unreads,originalUnread);
});
test('notifications blocked only for configured workspace while hidden', async () => {
  const {g,ipc,invoked,work,personal}=setup();
  g.apply(true);
  await ipc.invoke('SLACK_NOTIFY',{teamId:work});
  await ipc.invoke('SLACK_NOTIFY',{launchUri:'slack://channel?team='+work});
  assert.equal(invoked.length,0);
  await ipc.invoke('SLACK_NOTIFY',{teamId:personal});
  await ipc.invoke('SLACK_NOTIFY',{launchUri:'invalid'});
  await ipc.invoke('OTHER_CHANNEL',{teamId:work});
  assert.equal(invoked.length,3);
  g.apply(false);
  await ipc.invoke('SLACK_NOTIFY',{teamId:work});
  assert.equal(invoked.length,4);
});
test('restoration uses latest app data, not stale initial snapshot', () => {
  const {g,ipc,state,work,personal}=setup();
  g.apply(true);
  ipc.send('SYNC_WORKSPACES',{[personal]:{id:personal,order:0},[work]:{id:work,order:2},TNEW:{id:'TNEW',order:1}});
  ipc.send('REDUX_UPDATE_FROM_WEBAPP',{teams:{[work]:{unreads:{showBullet:true,unreadHighlights:9,unreads:11},locale:'en'}}});
  assert.equal(state.workspaces[work],undefined);
  assert.equal(state.webapp.teams[work].unreads.unreads,0);
  assert.equal(state.webapp.teams[work].locale,'en');
  g.apply(false);
  assert.equal(state.workspaces.TNEW.id,'TNEW');
  assert.equal(state.webapp.teams[work].unreads.unreadHighlights,9);
});
test('sparse updates and other workspaces remain untouched', () => {
  const {g,ipc,sent,work,personal}=setup();g.apply(true);
  const payload={teams:{[personal]:{unreads:{unreads:7}},[work]:{locale:'fi'}}};
  ipc.send('REDUX_UPDATE_FROM_WEBAPP',payload);
  assert.equal(sent.at(-1)[1],payload);
});
test('install is idempotent and self-test does not deliver a notification', async () => {
  const {g,context,ipc,invoked}=setup();const before=ipc.invoke;
  vm.runInNewContext('('+installNativeGuard.toString()+').call(electron,store,config)',context);
  assert.equal(before,ipc.invoke);g.apply(true);
  assert.ok(Object.values(await g.test()).every(Boolean));assert.equal(invoked.length,0);
});
test('guard status detects overwritten hooks', () => {
  const {g,ipc}=setup();ipc.invoke=()=>{};assert.equal(g.status().notificationHookIntact,false);
});

test('reload restores presentation metadata without losing current other workspaces', () => {
  const { g, context, ipc, state, work, personal, originalUnread, originalInvoke, originalSend } = setup();
  g.apply(true);
  const restore = { workspace: g.workspaces[work], unreads: g.unreads };
  assert.equal(state.workspaces[work], undefined);
  state.workspaces.TNEW = { id: 'TNEW', order: 1 };
  delete context.window.__slackWorkNative;
  ipc.invoke = originalInvoke;
  ipc.send = originalSend;
  context.restore = restore;
  vm.runInNewContext('(' + installNativeGuard.toString() + ').call(electron,store,config,restore)', context);
  const restored = context.window.__slackWorkNative;
  restored.apply(true);
  assert.deepEqual(Object.keys(state.workspaces), [personal, 'TNEW']);
  restored.apply(false);
  assert.equal(state.workspaces[work].id, work);
  assert.equal(state.workspaces.TNEW.id, 'TNEW');
  assert.deepEqual(state.webapp.teams[work].unreads, originalUnread);
  assert.deepEqual(Object.values(state.workspaces).map(w => w.order).sort(), [0, 1, 2]);
});
