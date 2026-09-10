package deps

import "github.com/citeck/citeck-launcher/internal/appdef"

// rabbitDescriptor describes RabbitMQ. A minor bump moves the data and cannot
// be undone, so it is breaking; which minor bumps the VENDOR permits at all is
// a separate question, answered by UpgradeSupport from the whitelist below.
type rabbitDescriptor struct{}

func (rabbitDescriptor) ID() ID          { return RabbitMQ }
func (rabbitDescriptor) AppName() string { return appdef.AppRabbitmq }
func (rabbitDescriptor) ParseVersion(image string) (Version, bool) {
	return ParseImageVersion(image)
}
func (rabbitDescriptor) VolumeBase() string               { return "rabbitmq" }
func (rabbitDescriptor) IsBreaking(from, to Version) bool { return minorBreaking(from, to) }
func (rabbitDescriptor) Migratable() bool                 { return true }
func (rabbitDescriptor) LegacyImage() string              { return "rabbitmq:4.1-management" }

// rabbitUpgradePaths is RabbitMQ's published upgrade matrix: for each release
// series, the series its data may move to in ONE hop. It is a WHITELIST and
// not a "next minor only" rule — 3.13 may go straight to 4.2, while 4.1 may
// NOT go to 4.3 — so it has to be written out.
//
// Keyed on (major, minor) only. The one published constraint finer than that
// is 3.11.18 → 3.12 (the vendor requires 3.11.18+); this launcher's bundles
// have never shipped 3.11, so the patch floor is documented here rather than
// modeled. Everything else in the matrix is series-to-series.
//
// WHEN A NEW SERIES SHIPS it must be added here, or its upgrade is reported as
// having no vendor-documented path. That is the safe direction: the
// alternative is offering a hop the vendor has not published.
var rabbitUpgradePaths = map[Version][]Version{
	{Major: 3, Minor: 11}: {{Major: 3, Minor: 12}},
	{Major: 3, Minor: 12}: {{Major: 3, Minor: 13}},
	{Major: 3, Minor: 13}: {{Major: 4, Minor: 0}, {Major: 4, Minor: 1}, {Major: 4, Minor: 2}},
	{Major: 4, Minor: 0}:  {{Major: 4, Minor: 1}, {Major: 4, Minor: 2}},
	{Major: 4, Minor: 1}:  {{Major: 4, Minor: 2}},
	{Major: 4, Minor: 2}:  {{Major: 4, Minor: 3}},
}

// UpgradeSupport answers RabbitMQ's own matrix.
//
// Not modeled, deliberately: 3.13-with-Khepri → any 4.x, which the vendor
// answers with blue-green only. It is a property of the DATA and not of the
// versions, so a matrix keyed on versions alone cannot express it — and 3.13
// is not a series this launcher's bundles ship, so nothing reaches that pair.
func (rabbitDescriptor) UpgradeSupport(from, to Version) VendorSupport {
	return seriesSupport(rabbitUpgradePaths, from, to)
}
