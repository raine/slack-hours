(() => {
  const ready = () => !!document.querySelector('[data-qa="team_sidebar_item"]');
  if (ready()) return true;
  if (!window.__slackWorkReadiness) {
    const observer = new MutationObserver(() => {
      if (!ready()) return;
      observer.disconnect();
      delete window.__slackWorkReadiness;
      window.__slackWorkReadySignal('ready');
    });
    window.__slackWorkReadiness = observer;
    observer.observe(document, { childList: true, subtree: true });
  }
  return false;
})()
