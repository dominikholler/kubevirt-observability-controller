/*
This file is part of the KubeVirt project

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.

Copyright The KubeVirt Authors.
*/

package nad

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	nadv1 "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"
	"k8s.io/client-go/tools/cache"
)

var _ = Describe("Network attachment definition collector", func() {
	It("reports nothing when the informer store is missing", func() {
		collector := NewCollector(func() cache.Store { return nil })
		Expect(collector.CollectCallback()).To(BeEmpty())
	})

	It("reports nothing for an object that is not a NAD or has no config", func() {
		store := cache.NewStore(func(obj any) (string, error) {
			return "key", nil
		})
		Expect(store.Add("not-a-nad")).To(Succeed())
		collector := NewCollector(func() cache.Store { return store })
		Expect(collector.CollectCallback()).To(BeEmpty())
		Expect(describe(nadObject("ns", "empty", "", nil))).To(BeEmpty())
		Expect(describe(nadObject("ns", "bad", `{not-json`, map[string]string{
			"k8s.ovn.org/user-defined-network": "",
		}))).To(BeEmpty())
	})

	It("reports one series per distinct plugin label set", func() {
		config := `{"cniVersion":"0.3.1","plugins":[` +
			`{"type":"tuning"},` +
			`{"type":"tuning","mtu":1500},` +
			`{"type":"bridge","vlan":100}` +
			`]}`
		got := report([]*nadv1.NetworkAttachmentDefinition{
			nadObject("ns", "br", config, nil),
		})
		Expect(got).To(HaveLen(2))
		Expect(got[0].Labels[3]).To(Equal("tuning"))
		Expect(got[1].Labels[3]).To(Equal("bridge"))
	})

	It("reports one series per plugin in a conflist", func() {
		config := `{"cniVersion":"0.3.1","plugins":[` +
			`{"type":"tuning"},` +
			`{"type":"bridge","vlan":100}` +
			`]}`
		got := describe(nadObject("ns", "br", config, nil))
		Expect(got).To(HaveLen(2))
		Expect(got[0].cniType).To(Equal("tuning"))
		Expect(got[0].vlan).To(BeEmpty())
		Expect(got[1].labels()).To(Equal([]string{
			"ns", "br", "100", cniBridge, "", "", "", "", "", "", "", "",
		}))
	})

	DescribeTable("parses the design-doc NAD examples",
		func(namespace, name, config string, labels map[string]string, want []string) {
			got := describe(nadObject(namespace, name, config, labels))
			Expect(got).To(HaveLen(1))
			Expect(got[0].labels()).To(Equal(want))
		},
		Entry("OVN layer2 primary UDN",
			"demo", "l2-primary",
			`{"allowPersistentIPs":true,"cniVersion":"1.0.0",`+
				`"name":"cluster_udn_l2-primary","role":"primary",`+
				`"subnets":"10.0.0.0/16,2001:db8::/60","topology":"layer2",`+
				`"type":"ovn-k8s-cni-overlay"}`,
			map[string]string{"k8s.ovn.org/user-defined-network": ""},
			[]string{
				"demo", "l2-primary", "", ovnOverlayCNI, "",
				"10.0.0.0/16,2001:db8::/60", "primary", "layer2", "true",
				"", "", "",
			},
		),
		Entry("OVN without role, subnets, or persistent IPs leaves those labels empty",
			"demo", "l2-network",
			`{"type":"ovn-k8s-cni-overlay","topology":"layer2"}`,
			nil,
			[]string{
				"demo", "l2-network", "", ovnOverlayCNI, "",
				"", "", "layer2", "",
				"", "", "",
			},
		),
		Entry("OVN localnet reports vlanID and is not a UDN",
			"default", "localnet-network",
			`{"cniVersion":"0.3.1","name":"localnet-network",`+
				`"type":"ovn-k8s-cni-overlay","topology":"localnet","vlanID":100}`,
			nil,
			[]string{
				"default", "localnet-network", "100", ovnOverlayCNI, "",
				"", "", "localnet", "",
				"", "", "",
			},
		),
		Entry("bridge reports vlan and bridge defaults overridden by the config",
			"default", "bridge-network",
			`{"cniVersion":"0.3.1","name":"bridge-network","type":"bridge",`+
				`"bridge":"br1","macspoofchk":false,"vlan":100,`+
				`"disableContainerInterface":true,"preserveDefaultVlan":false}`,
			nil,
			[]string{
				"default", "bridge-network", "100", cniBridge, "",
				"", "", "", "",
				"false", "false", "true",
			},
		),
		Entry("bridge without optional fields leaves them empty",
			"default", "bridge-defaults",
			`{"type":"bridge"}`,
			nil,
			[]string{
				"default", "bridge-defaults", "", cniBridge, "",
				"", "", "", "",
				"", "", "",
			},
		),
		Entry("sriov reports spoofchk and the ipam type",
			"default", "sriov-net1",
			`{"type":"sriov","cniVersion":"0.3.1","spoofchk":"off","vlan":100,`+
				`"ipam":{"type":"host-local","subnet":"10.56.217.0/24"}}`,
			nil,
			[]string{
				"default", "sriov-net1", "100", cniSRIOV, "host-local",
				"", "", "", "",
				"off", "", "",
			},
		),
		Entry("sriov without spoofchk leaves it empty",
			"default", "sriov-defaults",
			`{"type":"sriov"}`,
			nil,
			[]string{
				"default", "sriov-defaults", "", cniSRIOV, "",
				"", "", "", "",
				"", "", "",
			},
		),
		Entry("an empty ipam object has no type",
			"default", "no-ipam",
			`{"type":"macvlan","ipam":{}}`,
			nil,
			[]string{
				"default", "no-ipam", "", "macvlan", "",
				"", "", "", "",
				"", "", "",
			},
		),
		Entry("cnv-bridge reports only the bridge fields that are set",
			"default", "cnv",
			`{"type":"cnv-bridge","vlan":50,"macspoofchk":true}`,
			nil,
			[]string{
				"default", "cnv", "50", cniCNVBridge, "",
				"", "", "", "",
				"true", "", "",
			},
		),
	)
})

func nadObject(
	namespace, name, config string, labels map[string]string,
) *nadv1.NetworkAttachmentDefinition {
	obj := &nadv1.NetworkAttachmentDefinition{}
	obj.Namespace = namespace
	obj.Name = name
	if len(labels) > 0 {
		obj.Labels = labels
	}
	obj.Spec.Config = config
	return obj
}
