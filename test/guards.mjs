import { readFileSync } from 'node:fs';
const source = name => readFileSync(new URL('../cmd/slack-hours/hooks/' + name + '.js', import.meta.url), 'utf8');
export const installNativeGuard = Function('return (' + source('native') + ')')();
export const installWebGuard = Function('return (' + source('web') + ')')();
export const soundPatch = source('sound');
