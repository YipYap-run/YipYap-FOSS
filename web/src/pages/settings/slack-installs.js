import { signal } from '@preact/signals';

/* Last GET /integrations/slack/installations response, shared by the Slack
   workspaces section and the channel list so a disconnected workspace shows
   on the channels that use it. null until loaded, and on self-hosted builds. */
export const slackInstallsData = signal(null);
