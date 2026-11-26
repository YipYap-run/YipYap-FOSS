package sqlx

// Extra cleanup statements for tables that only edition-specific features
// write and that neither carry a foreign key to their parent nor sit in the
// generic org_id table list. Build-tagged files append to these from init.
// Each statement takes the deleted org or monitor id as its only argument.
var (
	// orgDeleteExtras run at the start of OrgStore.Delete.
	orgDeleteExtras []string
	// monitorDeleteExtras run in MonitorStore.Delete before its alerts go.
	monitorDeleteExtras []string
)
