package cloudevents

import (
	"time"

	ce "github.com/cloudevents/sdk-go/v2"
	"github.com/oklog/ulid/v2"

	"github.com/YipYap-run/YipYap-FOSS/internal/cloudevents/types"
)

const publicBaseURL = "https://console.yipyap.run"

// OrgSource returns the canonical event source URI for an org.
func OrgSource(orgID string) string { return publicBaseURL + "/orgs/" + orgID }

// MonitorSource returns the canonical source URI for a monitor.
func MonitorSource(orgID, monitorID string) string {
	return OrgSource(orgID) + "/monitors/" + monitorID
}

// newEvent builds a CloudEvent 1.0 with yipyap's required envelope.
// Callers pass the typed `*Data` struct; this function serializes it
// into the CloudEvent's data field as JSON.
func newEvent(typ, source, subject string, data any) (ce.Event, error) {
	ev := ce.NewEvent()
	ev.SetSpecVersion("1.0")
	ev.SetID(ulid.Make().String())
	ev.SetSource(source)
	ev.SetType(typ)
	ev.SetSubject(subject)
	ev.SetTime(time.Now().UTC())
	if err := ev.SetData(ce.ApplicationJSON, data); err != nil {
		return ev, err
	}
	return ev, nil
}

// NewAlertFiredV1 builds a run.yipyap.alert.fired.v1 event.
func NewAlertFiredV1(source string, d types.AlertFiredV1Data) (ce.Event, error) {
	return newEvent(types.TypeAlertFiredV1, source, "alert/"+d.AlertID, d)
}

// NewAlertAcknowledgedV1 builds a run.yipyap.alert.acknowledged.v1 event.
func NewAlertAcknowledgedV1(source string, d types.AlertAcknowledgedV1Data) (ce.Event, error) {
	return newEvent(types.TypeAlertAcknowledgedV1, source, "alert/"+d.AlertID, d)
}

// NewAlertResolvedV1 builds a run.yipyap.alert.resolved.v1 event.
func NewAlertResolvedV1(source string, d types.AlertResolvedV1Data) (ce.Event, error) {
	return newEvent(types.TypeAlertResolvedV1, source, "alert/"+d.AlertID, d)
}

// NewAlertEscalatedV1 builds a run.yipyap.alert.escalated.v1 event.
func NewAlertEscalatedV1(source string, d types.AlertEscalatedV1Data) (ce.Event, error) {
	return newEvent(types.TypeAlertEscalatedV1, source, "alert/"+d.AlertID, d)
}

// NewMonitorUpV1 builds a run.yipyap.monitor.up.v1 event.
func NewMonitorUpV1(source string, d types.MonitorUpV1Data) (ce.Event, error) {
	return newEvent(types.TypeMonitorUpV1, source, "monitor/"+d.MonitorID, d)
}

// NewMonitorDownV1 builds a run.yipyap.monitor.down.v1 event.
func NewMonitorDownV1(source string, d types.MonitorDownV1Data) (ce.Event, error) {
	return newEvent(types.TypeMonitorDownV1, source, "monitor/"+d.MonitorID, d)
}

// NewMonitorDegradedV1 builds a run.yipyap.monitor.degraded.v1 event.
func NewMonitorDegradedV1(source string, d types.MonitorDegradedV1Data) (ce.Event, error) {
	return newEvent(types.TypeMonitorDegradedV1, source, "monitor/"+d.MonitorID, d)
}

// NewMonitorHeartbeatMissedV1 builds a run.yipyap.monitor.heartbeat_missed.v1 event.
func NewMonitorHeartbeatMissedV1(source string, d types.MonitorHeartbeatMissedV1Data) (ce.Event, error) {
	return newEvent(types.TypeMonitorHeartbeatMissedV1, source, "monitor/"+d.MonitorID, d)
}

// NewMaintenanceStartedV1 builds a run.yipyap.maintenance.started.v1 event.
func NewMaintenanceStartedV1(source string, d types.MaintenanceStartedV1Data) (ce.Event, error) {
	return newEvent(types.TypeMaintenanceStartedV1, source, "maintenance/"+d.WindowID, d)
}

// NewMaintenanceEndedV1 builds a run.yipyap.maintenance.ended.v1 event.
func NewMaintenanceEndedV1(source string, d types.MaintenanceEndedV1Data) (ce.Event, error) {
	return newEvent(types.TypeMaintenanceEndedV1, source, "maintenance/"+d.WindowID, d)
}
