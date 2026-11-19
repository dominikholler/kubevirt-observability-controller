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
	"encoding/json"
	"strconv"
	"strings"

	nadv1 "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"
	"github.com/rhobs/operator-observability-toolkit/pkg/operatormetrics"
	"k8s.io/client-go/tools/cache"

	"github.com/kubevirt/kubevirt-observability-controller/pkg/monitoring/groups"
	"github.com/kubevirt/kubevirt-observability-controller/pkg/monitoring/metrics/internal/inventory"
)

const (
	ovnOverlayCNI = "ovn-k8s-cni-overlay"
	cniBridge     = "bridge"
	cniCNVBridge  = "cnv-bridge"
	cniSRIOV      = "sriov"
)

var networkAttachmentDefinitionInfo = operatormetrics.NewGaugeVec(
	operatormetrics.MetricOpts{
		Name:        "kubevirt_network_attachment_definition_info",
		Help:        "Details about additional network interfaces attached to Virtual Machines.",
		ExtraFields: groups.Tag(groups.Inventory),
	},
	[]string{
		"namespace",
		"network",
		"vlan",
		"cni_type",
		"ipam_type",
		"ovn_subnets",
		"udn_role",
		"ovn_topology",
		"ovn_persistent_ips",
		"mac_spoof_filtering",
		"bridge_preserving_default_vlan",
		"bridge_disable_container_interface",
	},
)

// NewCollector collects NetworkAttachmentDefinition metrics from the current informer store.
func NewCollector(getStore func() cache.Store) operatormetrics.Collector {
	return inventory.NewCollector(
		[]operatormetrics.Metric{networkAttachmentDefinitionInfo},
		getStore,
		report,
	)
}

type attachmentInfo struct {
	namespace           string
	network             string
	vlan                string
	cniType             string
	ipamType            string
	ovnSubnets          string
	udnRole             string
	ovnTopology         string
	ovnPersistentIPs    string
	macSpoofFiltering   string
	preserveDefaultVLAN string
	disableIface        string
}

func (a attachmentInfo) labels() []string {
	return []string{
		a.namespace,
		a.network,
		a.vlan,
		a.cniType,
		a.ipamType,
		a.ovnSubnets,
		a.udnRole,
		a.ovnTopology,
		a.ovnPersistentIPs,
		a.macSpoofFiltering,
		a.preserveDefaultVLAN,
		a.disableIface,
	}
}

func report(items []*nadv1.NetworkAttachmentDefinition) []operatormetrics.CollectorResult {
	results := make([]operatormetrics.CollectorResult, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		for _, info := range describe(item) {
			labels := info.labels()
			key := strings.Join(labels, "\x00")
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			results = append(results, operatormetrics.CollectorResult{
				Metric: networkAttachmentDefinitionInfo,
				Labels: labels,
				Value:  1,
			})
		}
	}
	return results
}

func describe(item *nadv1.NetworkAttachmentDefinition) []attachmentInfo {
	cfg, err := parseConfig(item.Spec.Config)
	if err != nil || cfg == nil {
		return nil
	}

	infos := make([]attachmentInfo, 0, 1)
	for _, plugin := range pluginConfigs(cfg) {
		infos = append(infos, extract(item.Namespace, item.Name, plugin))
	}
	return infos
}

func parseConfig(raw string) (map[string]any, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.UseNumber()
	var cfg map[string]any
	if err := dec.Decode(&cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

func pluginConfigs(cfg map[string]any) []map[string]any {
	raw, ok := cfg["plugins"]
	if !ok {
		return []map[string]any{cfg}
	}
	plugins, ok := raw.([]any)
	if !ok || len(plugins) == 0 {
		return []map[string]any{cfg}
	}
	configs := make([]map[string]any, 0, len(plugins))
	for _, item := range plugins {
		plugin, ok := item.(map[string]any)
		if !ok {
			continue
		}
		configs = append(configs, plugin)
	}
	if len(configs) == 0 {
		return []map[string]any{cfg}
	}
	return configs
}

func extract(namespace, network string, cfg map[string]any) attachmentInfo {
	info := attachmentInfo{
		namespace: namespace,
		network:   network,
	}
	info.cniType, _ = cfg["type"].(string)
	info.vlan = vlanName(cfg)
	info.ipamType = ipamType(cfg)
	if info.cniType != ovnOverlayCNI {
		info.macSpoofFiltering = macSpoofFiltering(info.cniType, cfg)
		info.preserveDefaultVLAN = bridgeBool(info.cniType, cfg, "preserveDefaultVlan")
		info.disableIface = bridgeBool(info.cniType, cfg, "disableContainerInterface")
		return info
	}

	info.ovnSubnets, _ = cfg["subnets"].(string)
	info.udnRole, _ = cfg["role"].(string)
	info.ovnTopology, _ = cfg["topology"].(string)
	info.ovnPersistentIPs = optionalBool(cfg, "allowPersistentIPs")
	return info
}

func vlanName(cfg map[string]any) string {
	if v, ok := cfg["vlan"]; ok {
		if name := scalarString(v); name != "" {
			return name
		}
	}
	if v, ok := cfg["vlanID"]; ok {
		return scalarString(v)
	}
	return ""
}

func ipamType(cfg map[string]any) string {
	raw, ok := cfg["ipam"]
	if !ok || raw == nil {
		return ""
	}
	ipam, ok := raw.(map[string]any)
	if !ok {
		return ""
	}
	ipamType, _ := ipam["type"].(string)
	return ipamType
}

func macSpoofFiltering(cniType string, cfg map[string]any) string {
	switch cniType {
	case cniBridge, cniCNVBridge:
		return optionalBool(cfg, "macspoofchk")
	case cniSRIOV:
		spoof, _ := cfg["spoofchk"].(string)
		return spoof
	default:
		return ""
	}
}

func bridgeBool(cniType string, cfg map[string]any, field string) string {
	if cniType != cniBridge && cniType != cniCNVBridge {
		return ""
	}
	return optionalBool(cfg, field)
}

func optionalBool(cfg map[string]any, field string) string {
	value, ok := cfg[field].(bool)
	if !ok {
		return ""
	}
	return strconv.FormatBool(value)
}

func scalarString(v any) string {
	switch value := v.(type) {
	case string:
		return value
	case json.Number:
		return value.String()
	default:
		return ""
	}
}
