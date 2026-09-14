package debian

const DatastoreUnsupportedHost = "Network datastore runtime uses the Debian 13 adapter. This host is not Debian 13 amd64."

// DatastoreRuntimePackages are optional. They are not Depends of ndl-agent.
var DatastoreRuntimePackages = []string{"nfs-common", "cifs-utils", "open-iscsi"}
