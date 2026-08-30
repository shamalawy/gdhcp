package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/shamalawy/gdhcp/server/internal/cli/client"
	"github.com/shamalawy/gdhcp/server/internal/cli/common"
	"github.com/shamalawy/gdhcp/server/internal/models"
)

func newClusterCmd(flags *persistentFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "cluster",
		Short:   "Manage HA clusters and their member servers",
		Aliases: []string{"clusters"},
	}
	cmd.AddCommand(newClusterListCmd(flags))
	cmd.AddCommand(newClusterGetCmd(flags))
	cmd.AddCommand(newClusterCreateCmd(flags))
	cmd.AddCommand(newClusterUpdateCmd(flags))
	cmd.AddCommand(newClusterDeleteCmd(flags))
	cmd.AddCommand(newClusterServersCmd(flags))
	cmd.AddCommand(newClusterHealthCmd(flags))
	cmd.AddCommand(newClusterHeartbeatsCmd(flags))
	cmd.AddCommand(newClusterFailoverCmd(flags))
	cmd.AddCommand(newClusterBootstrapCmd(flags))
	return cmd
}

func clusterTable(clusters []*models.Cluster) ([]string, [][]string) {
	headers := []string{"ID", "NAME", "VIP_ADDRESS", "VIP_INTERFACE", "HEARTBEAT_MS", "DESCRIPTION"}
	rows := make([][]string, 0, len(clusters))
	for _, c := range clusters {
		vip := "-"
		if c.VIPAddress != nil {
			vip = c.VIPAddress.String()
		}
		rows = append(rows, []string{c.ID, c.Name, vip, common.Str(c.VIPInterface), fmt.Sprintf("%d", c.HeartbeatInterval), common.Str(c.Description)})
	}
	return headers, rows
}

func newClusterListCmd(flags *persistentFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List clusters",
		RunE: func(cmd *cobra.Command, args []string) error {
			rc, err := newRunContext(flags)
			if err != nil {
				return err
			}
			clusters, err := rc.client.ListClusters(rc.ctx, common.ListParams(cmd))
			if err != nil {
				return err
			}
			return common.Render(cmd.OutOrStdout(), rc.output, clusters, func() ([]string, [][]string) { return clusterTable(clusters) })
		},
	}
	common.AddListFlags(cmd)
	return cmd
}

func newClusterGetCmd(flags *persistentFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "get <cluster-id>",
		Short: "Get a single cluster's configuration",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rc, err := newRunContext(flags)
			if err != nil {
				return err
			}
			c, err := rc.client.GetCluster(rc.ctx, args[0])
			if err != nil {
				return err
			}
			return common.Render(cmd.OutOrStdout(), rc.output, c, func() ([]string, [][]string) { return clusterTable([]*models.Cluster{c}) })
		},
	}
}

func newClusterCreateCmd(flags *persistentFlags) *cobra.Command {
	var name, description, vipAddress, vipInterface string
	var heartbeatMs, ttlMs, electionMs int
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a cluster",
		RunE: func(cmd *cobra.Command, args []string) error {
			rc, err := newRunContext(flags)
			if err != nil {
				return err
			}
			req := client.ClusterCreateRequest{
				Name: name, VIPAddress: vipAddress, VIPInterface: vipInterface,
				HeartbeatIntervalMs: heartbeatMs, HeartbeatTTLMs: ttlMs, ElectionIntervalMs: electionMs,
			}
			if description != "" {
				req.Description = &description
			}
			c, err := rc.client.CreateCluster(rc.ctx, req)
			if err != nil {
				return err
			}
			return common.Render(cmd.OutOrStdout(), rc.output, c, func() ([]string, [][]string) { return clusterTable([]*models.Cluster{c}) })
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "Cluster name (required)")
	cmd.Flags().StringVar(&description, "description", "", "Description")
	cmd.Flags().StringVar(&vipAddress, "vip-address", "", "Virtual IP with prefix, e.g. 10.0.0.10/24 (required)")
	cmd.Flags().StringVar(&vipInterface, "vip-interface", "", "Interface the VIP is assigned to (required)")
	cmd.Flags().IntVar(&heartbeatMs, "heartbeat-interval-ms", 1000, "Heartbeat interval in ms")
	cmd.Flags().IntVar(&ttlMs, "heartbeat-ttl-ms", 3000, "Heartbeat TTL in ms (must be >= 2x interval)")
	cmd.Flags().IntVar(&electionMs, "election-interval-ms", 2000, "Election interval in ms")
	cmd.MarkFlagRequired("name")
	cmd.MarkFlagRequired("vip-address")
	cmd.MarkFlagRequired("vip-interface")
	return cmd
}

func newClusterUpdateCmd(flags *persistentFlags) *cobra.Command {
	var name, description string
	var heartbeatMs, ttlMs, electionMs int
	cmd := &cobra.Command{
		Use:   "update <cluster-id>",
		Short: "Update a cluster's configuration",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rc, err := newRunContext(flags)
			if err != nil {
				return err
			}
			req := client.ClusterUpdateRequest{
				Name:                common.StringFlagPtr(cmd, "name"),
				Description:         common.StringFlagPtr(cmd, "description"),
				HeartbeatIntervalMs: common.IntFlagPtr(cmd, "heartbeat-interval-ms"),
				HeartbeatTTLMs:      common.IntFlagPtr(cmd, "heartbeat-ttl-ms"),
				ElectionIntervalMs:  common.IntFlagPtr(cmd, "election-interval-ms"),
			}
			c, err := rc.client.UpdateCluster(rc.ctx, args[0], req)
			if err != nil {
				return err
			}
			return common.Render(cmd.OutOrStdout(), rc.output, c, func() ([]string, [][]string) { return clusterTable([]*models.Cluster{c}) })
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "New name")
	cmd.Flags().StringVar(&description, "description", "", "New description")
	cmd.Flags().IntVar(&heartbeatMs, "heartbeat-interval-ms", 0, "New heartbeat interval in ms")
	cmd.Flags().IntVar(&ttlMs, "heartbeat-ttl-ms", 0, "New heartbeat TTL in ms")
	cmd.Flags().IntVar(&electionMs, "election-interval-ms", 0, "New election interval in ms")
	return cmd
}

func newClusterDeleteCmd(flags *persistentFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "delete <cluster-id>",
		Short: "Delete a cluster (must have no servers)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rc, err := newRunContext(flags)
			if err != nil {
				return err
			}
			if err := rc.client.DeleteCluster(rc.ctx, args[0]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "cluster %s deleted\n", args[0])
			return nil
		},
	}
}

func newClusterServersCmd(flags *persistentFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "servers",
		Short: "List, register, or deregister cluster member servers",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "list <cluster-id>",
		Short: "List servers in a cluster",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rc, err := newRunContext(flags)
			if err != nil {
				return err
			}
			servers, err := rc.client.ClusterServers(rc.ctx, args[0])
			if err != nil {
				return err
			}
			return common.Render(cmd.OutOrStdout(), rc.output, servers, func() ([]string, [][]string) {
				headers := []string{"ID", "NODE_NAME", "NODE_ID", "API_ADDRESS", "DHCP_ADDRESS"}
				rows := make([][]string, 0, len(servers))
				for _, s := range servers {
					rows = append(rows, []string{s.ID, s.NodeName, fmt.Sprintf("%d", s.NodeID), s.APIAddress, s.DHCPAddress})
				}
				return headers, rows
			})
		},
	})

	var nodeName, apiAddress, dhcpAddress string
	var nodeID int
	registerCmd := &cobra.Command{
		Use:   "register <cluster-id>",
		Short: "Manually register a server in a cluster",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rc, err := newRunContext(flags)
			if err != nil {
				return err
			}
			req := client.ClusterServerCreateRequest{NodeName: nodeName, NodeID: nodeID, APIAddress: apiAddress, DHCPAddress: dhcpAddress}
			s, err := rc.client.RegisterClusterServer(rc.ctx, args[0], req)
			if err != nil {
				return err
			}
			return common.Render(cmd.OutOrStdout(), rc.output, s, func() ([]string, [][]string) {
				return []string{"ID", "NODE_NAME", "NODE_ID", "API_ADDRESS", "DHCP_ADDRESS"}, [][]string{{s.ID, s.NodeName, fmt.Sprintf("%d", s.NodeID), s.APIAddress, s.DHCPAddress}}
			})
		},
	}
	registerCmd.Flags().StringVar(&nodeName, "node-name", "", "Node name (required)")
	registerCmd.Flags().IntVar(&nodeID, "node-id", 0, "Node ID, election tie-breaker: lower wins (required)")
	registerCmd.Flags().StringVar(&apiAddress, "api-address", "", "This node's API address, host:port (required)")
	registerCmd.Flags().StringVar(&dhcpAddress, "dhcp-address", "", "This node's DHCP listener address, host:port (required)")
	registerCmd.MarkFlagRequired("node-name")
	registerCmd.MarkFlagRequired("node-id")
	registerCmd.MarkFlagRequired("api-address")
	registerCmd.MarkFlagRequired("dhcp-address")
	cmd.AddCommand(registerCmd)

	cmd.AddCommand(&cobra.Command{
		Use:   "deregister <cluster-id> <server-id>",
		Short: "Deregister a server from a cluster",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			rc, err := newRunContext(flags)
			if err != nil {
				return err
			}
			if err := rc.client.DeregisterClusterServer(rc.ctx, args[0], args[1]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "server %s deregistered from cluster %s\n", args[1], args[0])
			return nil
		},
	})

	return cmd
}

// clusterHealthTable renders a *models.ClusterHealth for `cluster health`
// and `ha status`/`ha failover`.
func clusterHealthTable(h *models.ClusterHealth) ([]string, [][]string) {
	headers := []string{"SERVER_ID", "NODE_NAME", "NODE_ID", "ROLE", "VIP", "HEALTHY", "HEARTBEAT_AGE_MS", "API_ADDRESS"}
	rows := make([][]string, 0, len(h.Servers)+2)
	rows = append(rows, []string{"# cluster_state=" + string(h.ClusterState), "local_role=" + string(h.LocalRole), "", "", "", "", "", ""})
	for _, s := range h.Servers {
		rows = append(rows, []string{s.ServerID, s.NodeName, fmt.Sprintf("%d", s.NodeID), string(s.Role), common.Bool(s.VIPAssigned), common.Bool(s.IsHealthy), fmt.Sprintf("%d", s.HeartbeatAgeMs), s.APIAddress})
	}
	if strings.TrimSpace(h.Notes) != "" {
		rows = append(rows, []string{"# notes", h.Notes, "", "", "", "", "", ""})
	}
	return headers, rows
}

func newClusterHealthCmd(flags *persistentFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "health <cluster-id>",
		Short: "Show a cluster's health status",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rc, err := newRunContext(flags)
			if err != nil {
				return err
			}
			h, err := rc.client.ClusterHealth(rc.ctx, args[0])
			if err != nil {
				return err
			}
			return common.Render(cmd.OutOrStdout(), rc.output, h, func() ([]string, [][]string) { return clusterHealthTable(h) })
		},
	}
}

func newClusterHeartbeatsCmd(flags *persistentFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "heartbeats <cluster-id>",
		Short: "Show all heartbeat records for a cluster",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rc, err := newRunContext(flags)
			if err != nil {
				return err
			}
			hbs, err := rc.client.ClusterHeartbeats(rc.ctx, args[0])
			if err != nil {
				return err
			}
			return common.Render(cmd.OutOrStdout(), rc.output, hbs, func() ([]string, [][]string) {
				headers := []string{"SERVER_ID", "ROLE", "VIP_ASSIGNED", "HEARTBEAT_TIME", "LEASE_VERSION", "LAST_CONFIG_VERSION"}
				rows := make([][]string, 0, len(hbs))
				for _, hb := range hbs {
					rows = append(rows, []string{hb.ServerID, string(hb.Role), common.Bool(hb.VIPAssigned), common.Time(hb.HeartbeatTime), fmt.Sprintf("%d", hb.LeaseVersion), fmt.Sprintf("%d", hb.LastConfigVersion)})
				}
				return headers, rows
			})
		},
	}
}

func newClusterFailoverCmd(flags *persistentFlags) *cobra.Command {
	var target string
	cmd := &cobra.Command{
		Use:   "failover <cluster-id>",
		Short: "Manually trigger a role change to another server (admin)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rc, err := newRunContext(flags)
			if err != nil {
				return err
			}
			resp, err := rc.client.Failover(rc.ctx, args[0], target)
			if err != nil {
				return err
			}
			return common.Render(cmd.OutOrStdout(), rc.output, resp, func() ([]string, [][]string) {
				return []string{"MESSAGE", "NEW_PRIMARY"}, [][]string{{resp.Message, resp.NewPrimary}}
			})
		},
	}
	cmd.Flags().StringVar(&target, "target-server-id", "", "Server ID to promote to primary (required)")
	cmd.MarkFlagRequired("target-server-id")
	return cmd
}

func newClusterBootstrapCmd(flags *persistentFlags) *cobra.Command {
	var serverIDs []string
	cmd := &cobra.Command{
		Use:   "bootstrap <cluster-id>",
		Short: "Initialize a new cluster from a set of pre-registered server IDs",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rc, err := newRunContext(flags)
			if err != nil {
				return err
			}
			msg, err := rc.client.BootstrapCluster(rc.ctx, args[0], serverIDs)
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), msg)
			return nil
		},
	}
	cmd.Flags().StringSliceVar(&serverIDs, "server-id", nil, "Server ID to include (repeatable)")
	cmd.MarkFlagRequired("server-id")
	return cmd
}
