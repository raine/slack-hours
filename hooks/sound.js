(() => {
  const g = window.__slackWorkWeb;
  if (g.soundPatched) return true;
  const names = { canPlaySound, canPlayMessageSound, getHuddleInviteSoundPath,
    getSavedItemReminderSoundPath, getNewMsgSoundPath, getShouldMuteCalendarSounds,
    shouldShowNotificationForHuddle, shouldShowNotificationForChannelMessage,
    shouldShowNotificationForDirectMessage, shouldShowNotificationForDesktopNotification,
    areGlobalDesktopNotificationsDisabled };
  if (Object.values(names).some(fn => typeof fn !== 'function')) throw new Error('Unsupported sound module');
  canPlaySound = g.wrap(canPlaySound, false);
  canPlayMessageSound = g.wrap(canPlayMessageSound, false);
  getHuddleInviteSoundPath = g.wrap(getHuddleInviteSoundPath, undefined);
  getSavedItemReminderSoundPath = g.wrap(getSavedItemReminderSoundPath, undefined);
  getNewMsgSoundPath = g.wrap(getNewMsgSoundPath, undefined);
  getShouldMuteCalendarSounds = g.wrap(getShouldMuteCalendarSounds, true);
  shouldShowNotificationForHuddle = g.wrap(shouldShowNotificationForHuddle, 14);
  shouldShowNotificationForChannelMessage = g.wrap(shouldShowNotificationForChannelMessage, 14);
  shouldShowNotificationForDirectMessage = g.wrap(shouldShowNotificationForDirectMessage, 14);
  shouldShowNotificationForDesktopNotification = g.wrap(shouldShowNotificationForDesktopNotification, 14);
  areGlobalDesktopNotificationsDisabled = g.wrap(areGlobalDesktopNotificationsDisabled, true);
  g.patchedFunctions = { canPlaySound, canPlayMessageSound, getHuddleInviteSoundPath,
    getSavedItemReminderSoundPath, getNewMsgSoundPath, getShouldMuteCalendarSounds };
  g.soundPatched = true;
  return true;
})()
