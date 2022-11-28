package labels

import "github.com/onsi/ginkgo/v2"

var (
	Informing = ginkgo.Label("Informing")
	Blocking  = ginkgo.Label("Blocking")

	OSD  = ginkgo.Label("OSD")
	ROSA = ginkgo.Label("ROSA")

	HyperShift  = ginkgo.Label("HyperShift")
	STS         = ginkgo.Label("STS")
	PrivateLink = ginkgo.Label("PrivateLink")
)
