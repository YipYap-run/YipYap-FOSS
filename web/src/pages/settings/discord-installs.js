import { signal } from '@preact/signals';

/* Last GET /integrations/discord/installations response, shared by the
   Discord servers section and the channel list so a disconnected server
   shows on the channels that use it. null until loaded, and on self-hosted
   builds. */
export const discordInstallsData = signal(null);
