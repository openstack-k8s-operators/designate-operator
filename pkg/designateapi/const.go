package designateapi

const (
	// ServiceName -
	ServiceName = "designate"
	// ServiceType -
	ServiceType = "network"
	// KollaConfigAPI -
	KollaConfigAPI = "/var/lib/config-data/merged/designate-api-config.json"
	// KollaConfigDbSync -
	KollaConfigDbSync = "/var/lib/config-data/merged/db-sync-config.json"
	// ServiceAccountName -
	ServiceAccountName = "designate-operator-designate"
	// Database -
	Database = "designate"

	// DesignateAdminPort -
	DesignateAdminPort int32 = 9611
	// DesignatePublicPort -
	DesignatePublicPort int32 = 9611
	// DesignateInternalPort -
	DesignateInternalPort int32 = 9611
)
