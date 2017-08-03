package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"time"

	"code.cloudfoundry.org/loggregator/metricemitter"
	"code.cloudfoundry.org/loggregator/plumbing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/grpclog"

	"code.cloudfoundry.org/loggregator/trafficcontroller/app"
)

func main() {
	grpclog.SetLogger(log.New(ioutil.Discard, "", 0))
	disableAccessControl := flag.Bool(
		"disableAccessControl",
		false,
		"always all access to app logs",
	)
	configFile := flag.String(
		"config",
		"config/loggregator_trafficcontroller.json",
		"Location of the loggregator trafficcontroller config json file",
	)

	flag.Parse()

	conf, err := app.ParseConfig(*configFile)
	if err != nil {
		log.Panicf("Unable to parse config: %s", err)
	}

	credentials, err := plumbing.NewClientCredentials(
		conf.GRPC.CertFile,
		conf.GRPC.KeyFile,
		conf.GRPC.CAFile,
		"metron",
	)
	if err != nil {
		log.Fatalf("Could not use GRPC creds for client: %s", err)
	}

	// metric-documentation-v2: setup function
	metricClient, err := metricemitter.NewClient(
		conf.MetronConfig.GRPCAddress,
		metricemitter.WithGRPCDialOptions(grpc.WithTransportCredentials(credentials)),
		metricemitter.WithOrigin("loggregator.trafficcontroller"),
		metricemitter.WithPulseInterval(conf.MetricEmitterDuration),
	)
	if err != nil {
		log.Fatalf("Couldn't connect to metric emitter: %s", err)
	}

	go func() {
		err := oauth2Mockup(conf)
		if err != nil {
			log.Fatalf("Can't run oauth2Mockup: %s", err)
		}
	}()

	tc := app.NewTrafficController(
		conf,
		*disableAccessControl,
		metricClient,
		uaaHTTPClient(conf),
		ccHTTPClient(conf),
	)
	tc.Start()
}

func uaaHTTPClient(conf *app.Config) *http.Client {
	tlsConfig := plumbing.NewTLSConfig()
	tlsConfig.InsecureSkipVerify = conf.SkipCertVerify
	transport := &http.Transport{
		TLSHandshakeTimeout: 10 * time.Second,
		TLSClientConfig:     tlsConfig,
		DisableKeepAlives:   true,
	}
	return &http.Client{
		Timeout:   20 * time.Second,
		Transport: transport,
	}
}

func ccHTTPClient(conf *app.Config) *http.Client {
	tlsConfig, err := plumbing.NewClientMutualTLSConfig(
		conf.CCTLSClientConfig.CertFile,
		conf.CCTLSClientConfig.KeyFile,
		conf.CCTLSClientConfig.CAFile,
		conf.CCTLSClientConfig.ServerName,
	)
	if err != nil {
		log.Fatalf("Unable to create CC HTTP Client: %s", err)
	}
	tlsConfig.InsecureSkipVerify = conf.SkipCertVerify
	transport := &http.Transport{
		TLSHandshakeTimeout: 10 * time.Second,
		TLSClientConfig:     tlsConfig,
		DisableKeepAlives:   true,
	}
	return &http.Client{
		Timeout:   20 * time.Second,
		Transport: transport,
	}
}

func oauth2Mockup(conf *app.Config) error {
	// /v2/info
	info := []byte(`{"name":"","build":"","support":"https://support.pivotal.io","version":0,"description":"","authorization_endpoint":"","token_endpoint":"","min_cli_version":"6.23.0","min_recommended_cli_version":"6.23.0","api_version":"2.82.0","app_ssh_endpoint":"","app_ssh_host_key_fingerprint":"f3:f1:53:6d:dd:a3:94:37:0a:f8:ab:2b:3e:f7:56:27","app_ssh_oauth_client":"ssh-proxy","routing_endpoint":"","doppler_logging_endpoint":"","user":"5404b6b1-d8da-4f94-bcdf-1b78d8fed7eb"}`)
	var v map[string]interface{}
	err := json.Unmarshal(info, &v)
	if err != nil {
		return err
	}

	v["authorization_endpoint"] = conf.AuthorizationEndpoint
	v["token_endpoint"] = conf.TokenEndpoint
	v["doppler_logging_endpoint"] = conf.DopplerLoggingEndpoint

	info, err = json.Marshal(v)
	if err != nil {
		return err
	}

	http.HandleFunc("/v2/info", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, string(info))
	})

	// Authorization code endpoint
	http.HandleFunc("/oauth/auth", func(w http.ResponseWriter, r *http.Request) {
	})

	// Access token endpoint
	http.HandleFunc("/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		// Should return acccess token back to the user
		w.Header().Set("Content-Type", "application/x-www-form-urlencoded")
		w.Write([]byte("access_token=mocktoken&scope=user&token_type=bearer"))
	})

	apps := `{"total_results":1,"total_pages":1,"prev_url":null,"next_url":null,"resources":[{"metadata":{"guid":"913339ce-e25f-4193-8665-bfaf2d77970a","url":"/v2/apps/913339ce-e25f-4193-8665-bfaf2d77970a","created_at":"2017-06-14T15:51:25Z","updated_at":"2017-06-14T15:51:32Z"},"entity":{"name":"perf-mockup-app","production":false,"space_guid":"780d8010-2c8b-4b0c-999c-d9f806731b45","stack_guid":"2099b8b8-fee8-4bd5-ac25-22cee66a81b2","buildpack":"python_buildpack","detected_buildpack":"","detected_buildpack_guid":"b3c8b3ac-cbf3-408e-86f1-efdce139cae9","environment_json":{},"memory":64,"instances":1,"disk_quota":1024,"state":"STARTED","version":"4713b682-07ab-49ee-bc0a-3dfac7e5c4cb","command":null,"console":false,"debug":null,"staging_task_id":"e82d5f88-38d1-4373-baec-1965a21d0c30","package_state":"STAGED","health_check_type":"port","health_check_timeout":null,"health_check_http_endpoint":null,"staging_failed_reason":null,"staging_failed_description":null,"diego":true,"docker_image":null,"docker_credentials":{"username":null,"password":null},"package_updated_at":"2017-06-14T15:51:26Z","detected_start_command":"python index.py","enable_ssh":true,"ports":[8080],"space_url":"/v2/spaces/780d8010-2c8b-4b0c-999c-d9f806731b45","space":{"metadata":{"guid":"780d8010-2c8b-4b0c-999c-d9f806731b45","url":"/v2/spaces/780d8010-2c8b-4b0c-999c-d9f806731b45","created_at":"2017-06-14T15:47:45Z","updated_at":"2017-06-14T15:47:45Z"},"entity":{"name":"dev","organization_guid":"15fa6d39-97ce-48ac-be70-db0e86a9387a","space_quota_definition_guid":null,"isolation_segment_guid":null,"allow_ssh":true,"organization_url":"/v2/organizations/15fa6d39-97ce-48ac-be70-db0e86a9387a","organization":{"metadata":{"guid":"15fa6d39-97ce-48ac-be70-db0e86a9387a","url":"/v2/organizations/15fa6d39-97ce-48ac-be70-db0e86a9387a","created_at":"2017-06-14T15:47:37Z","updated_at":"2017-06-14T15:47:37Z"},"entity":{"name":"kchen","billing_enabled":false,"quota_definition_guid":"049b870b-bc96-423a-ad2e-8efb8807b9b6","status":"active","default_isolation_segment_guid":null,"quota_definition_url":"/v2/quota_definitions/049b870b-bc96-423a-ad2e-8efb8807b9b6","spaces_url":"/v2/organizations/15fa6d39-97ce-48ac-be70-db0e86a9387a/spaces","domains_url":"/v2/organizations/15fa6d39-97ce-48ac-be70-db0e86a9387a/domains","private_domains_url":"/v2/organizations/15fa6d39-97ce-48ac-be70-db0e86a9387a/private_domains","users_url":"/v2/organizations/15fa6d39-97ce-48ac-be70-db0e86a9387a/users","managers_url":"/v2/organizations/15fa6d39-97ce-48ac-be70-db0e86a9387a/managers","billing_managers_url":"/v2/organizations/15fa6d39-97ce-48ac-be70-db0e86a9387a/billing_managers","auditors_url":"/v2/organizations/15fa6d39-97ce-48ac-be70-db0e86a9387a/auditors","app_events_url":"/v2/organizations/15fa6d39-97ce-48ac-be70-db0e86a9387a/app_events","space_quota_definitions_url":"/v2/organizations/15fa6d39-97ce-48ac-be70-db0e86a9387a/space_quota_definitions"}},"developers_url":"/v2/spaces/780d8010-2c8b-4b0c-999c-d9f806731b45/developers","developers":[{"metadata":{"guid":"5404b6b1-d8da-4f94-bcdf-1b78d8fed7eb","url":"/v2/users/5404b6b1-d8da-4f94-bcdf-1b78d8fed7eb","created_at":"2017-06-07T17:39:02Z","updated_at":"2017-06-07T17:39:02Z"},"entity":{"admin":false,"active":true,"default_space_guid":null,"spaces_url":"/v2/users/5404b6b1-d8da-4f94-bcdf-1b78d8fed7eb/spaces","organizations_url":"/v2/users/5404b6b1-d8da-4f94-bcdf-1b78d8fed7eb/organizations","managed_organizations_url":"/v2/users/5404b6b1-d8da-4f94-bcdf-1b78d8fed7eb/managed_organizations","billing_managed_organizations_url":"/v2/users/5404b6b1-d8da-4f94-bcdf-1b78d8fed7eb/billing_managed_organizations","audited_organizations_url":"/v2/users/5404b6b1-d8da-4f94-bcdf-1b78d8fed7eb/audited_organizations","managed_spaces_url":"/v2/users/5404b6b1-d8da-4f94-bcdf-1b78d8fed7eb/managed_spaces","audited_spaces_url":"/v2/users/5404b6b1-d8da-4f94-bcdf-1b78d8fed7eb/audited_spaces"}}],"managers_url":"/v2/spaces/780d8010-2c8b-4b0c-999c-d9f806731b45/managers","managers":[{"metadata":{"guid":"5404b6b1-d8da-4f94-bcdf-1b78d8fed7eb","url":"/v2/users/5404b6b1-d8da-4f94-bcdf-1b78d8fed7eb","created_at":"2017-06-07T17:39:02Z","updated_at":"2017-06-07T17:39:02Z"},"entity":{"admin":false,"active":true,"default_space_guid":null,"spaces_url":"/v2/users/5404b6b1-d8da-4f94-bcdf-1b78d8fed7eb/spaces","organizations_url":"/v2/users/5404b6b1-d8da-4f94-bcdf-1b78d8fed7eb/organizations","managed_organizations_url":"/v2/users/5404b6b1-d8da-4f94-bcdf-1b78d8fed7eb/managed_organizations","billing_managed_organizations_url":"/v2/users/5404b6b1-d8da-4f94-bcdf-1b78d8fed7eb/billing_managed_organizations","audited_organizations_url":"/v2/users/5404b6b1-d8da-4f94-bcdf-1b78d8fed7eb/audited_organizations","managed_spaces_url":"/v2/users/5404b6b1-d8da-4f94-bcdf-1b78d8fed7eb/managed_spaces","audited_spaces_url":"/v2/users/5404b6b1-d8da-4f94-bcdf-1b78d8fed7eb/audited_spaces"}}],"auditors_url":"/v2/spaces/780d8010-2c8b-4b0c-999c-d9f806731b45/auditors","auditors":[],"apps_url":"/v2/spaces/780d8010-2c8b-4b0c-999c-d9f806731b45/apps","routes_url":"/v2/spaces/780d8010-2c8b-4b0c-999c-d9f806731b45/routes","routes":[{"metadata":{"guid":"6873bf52-2976-426a-a326-c473bed29e96","url":"/v2/routes/6873bf52-2976-426a-a326-c473bed29e96","created_at":"2017-06-14T15:51:25Z","updated_at":"2017-06-14T15:51:25Z"},"entity":{"host":"kchen-headers","path":"","domain_guid":"fd41d865-0e43-4379-92c3-9f69a447987d","space_guid":"780d8010-2c8b-4b0c-999c-d9f806731b45","service_instance_guid":"2c4605d1-f573-4dda-a1b9-2ba8c9b6f968","port":null,"domain_url":"/v2/shared_domains/fd41d865-0e43-4379-92c3-9f69a447987d","space_url":"/v2/spaces/780d8010-2c8b-4b0c-999c-d9f806731b45","service_instance_url":"/v2/user_provided_service_instances/2c4605d1-f573-4dda-a1b9-2ba8c9b6f968","apps_url":"/v2/routes/6873bf52-2976-426a-a326-c473bed29e96/apps","route_mappings_url":"/v2/routes/6873bf52-2976-426a-a326-c473bed29e96/route_mappings"}},{"metadata":{"guid":"b054ee4a-aa57-4832-9e6d-389971a5a472","url":"/v2/routes/b054ee4a-aa57-4832-9e6d-389971a5a472","created_at":"2017-06-14T15:55:12Z","updated_at":"2017-06-14T15:55:12Z"},"entity":{"host":"kchen-route-service","path":"","domain_guid":"fd41d865-0e43-4379-92c3-9f69a447987d","space_guid":"780d8010-2c8b-4b0c-999c-d9f806731b45","service_instance_guid":null,"port":null,"domain_url":"/v2/shared_domains/fd41d865-0e43-4379-92c3-9f69a447987d","space_url":"/v2/spaces/780d8010-2c8b-4b0c-999c-d9f806731b45","apps_url":"/v2/routes/b054ee4a-aa57-4832-9e6d-389971a5a472/apps","route_mappings_url":"/v2/routes/b054ee4a-aa57-4832-9e6d-389971a5a472/route_mappings"}}],"domains_url":"/v2/spaces/780d8010-2c8b-4b0c-999c-d9f806731b45/domains","domains":[{"metadata":{"guid":"fd41d865-0e43-4379-92c3-9f69a447987d","url":"/v2/shared_domains/fd41d865-0e43-4379-92c3-9f69a447987d","created_at":"2017-06-07T16:46:32Z","updated_at":"2017-07-18T02:26:02Z"},"entity":{"name":"cfapps.pie-ci-1-11.cfplatformeng.com","router_group_guid":null,"router_group_type":null}},{"metadata":{"guid":"cd939ae2-ba39-4930-827d-687b0a1a3139","url":"/v2/shared_domains/cd939ae2-ba39-4930-827d-687b0a1a3139","created_at":"2017-07-23T01:05:23Z","updated_at":"2017-07-23T01:05:23Z"},"entity":{"name":"tcp.pie-ci-1-11.cfplatformeng.com","router_group_guid":"e2fe0cdf-cebf-406d-7f11-a8f2db2485a9","router_group_type":null}}],"service_instances_url":"/v2/spaces/780d8010-2c8b-4b0c-999c-d9f806731b45/service_instances","service_instances":[{"metadata":{"guid":"2c4605d1-f573-4dda-a1b9-2ba8c9b6f968","url":"/v2/user_provided_service_instances/2c4605d1-f573-4dda-a1b9-2ba8c9b6f968","created_at":"2017-06-15T15:35:56Z","updated_at":"2017-06-15T15:35:56Z"},"entity":{"name":"my-route-service","credentials":{},"space_guid":"780d8010-2c8b-4b0c-999c-d9f806731b45","type":"user_provided_service_instance","syslog_drain_url":"","route_service_url":"https://kchen-route-service.cfapps.pie-ci-1-11.cfplatformeng.com","space_url":"/v2/spaces/780d8010-2c8b-4b0c-999c-d9f806731b45","service_bindings_url":"/v2/user_provided_service_instances/2c4605d1-f573-4dda-a1b9-2ba8c9b6f968/service_bindings","service_keys_url":"/v2/user_provided_service_instances/2c4605d1-f573-4dda-a1b9-2ba8c9b6f968/service_keys","routes_url":"/v2/user_provided_service_instances/2c4605d1-f573-4dda-a1b9-2ba8c9b6f968/routes"}}],"app_events_url":"/v2/spaces/780d8010-2c8b-4b0c-999c-d9f806731b45/app_events","events_url":"/v2/spaces/780d8010-2c8b-4b0c-999c-d9f806731b45/events","security_groups_url":"/v2/spaces/780d8010-2c8b-4b0c-999c-d9f806731b45/security_groups","security_groups":[{"metadata":{"guid":"a19f8b2b-fb3a-4682-8ae9-6fc765ff9483","url":"/v2/security_groups/a19f8b2b-fb3a-4682-8ae9-6fc765ff9483","created_at":"2017-06-07T16:46:32Z","updated_at":"2017-06-07T16:46:32Z"},"entity":{"name":"default_security_group","rules":[{"protocol":"all","destination":"0.0.0.0-169.253.255.255"},{"protocol":"all","destination":"169.255.0.0-255.255.255.255"}],"running_default":true,"staging_default":true,"spaces_url":"/v2/security_groups/a19f8b2b-fb3a-4682-8ae9-6fc765ff9483/spaces","staging_spaces_url":"/v2/security_groups/a19f8b2b-fb3a-4682-8ae9-6fc765ff9483/staging_spaces"}}],"staging_security_groups_url":"/v2/spaces/780d8010-2c8b-4b0c-999c-d9f806731b45/staging_security_groups","staging_security_groups":[{"metadata":{"guid":"a19f8b2b-fb3a-4682-8ae9-6fc765ff9483","url":"/v2/security_groups/a19f8b2b-fb3a-4682-8ae9-6fc765ff9483","created_at":"2017-06-07T16:46:32Z","updated_at":"2017-06-07T16:46:32Z"},"entity":{"name":"default_security_group","rules":[{"protocol":"all","destination":"0.0.0.0-169.253.255.255"},{"protocol":"all","destination":"169.255.0.0-255.255.255.255"}],"running_default":true,"staging_default":true,"spaces_url":"/v2/security_groups/a19f8b2b-fb3a-4682-8ae9-6fc765ff9483/spaces","staging_spaces_url":"/v2/security_groups/a19f8b2b-fb3a-4682-8ae9-6fc765ff9483/staging_spaces"}}]}},"stack_url":"/v2/stacks/2099b8b8-fee8-4bd5-ac25-22cee66a81b2","stack":{"metadata":{"guid":"2099b8b8-fee8-4bd5-ac25-22cee66a81b2","url":"/v2/stacks/2099b8b8-fee8-4bd5-ac25-22cee66a81b2","created_at":"2017-06-07T16:46:32Z","updated_at":"2017-06-07T16:46:32Z"},"entity":{"name":"cflinuxfs2","description":"Cloud Foundry Linux-based filesystem"}},"routes_url":"/v2/apps/913339ce-e25f-4193-8665-bfaf2d77970a/routes","routes":[{"metadata":{"guid":"6873bf52-2976-426a-a326-c473bed29e96","url":"/v2/routes/6873bf52-2976-426a-a326-c473bed29e96","created_at":"2017-06-14T15:51:25Z","updated_at":"2017-06-14T15:51:25Z"},"entity":{"host":"kchen-headers","path":"","domain_guid":"fd41d865-0e43-4379-92c3-9f69a447987d","space_guid":"780d8010-2c8b-4b0c-999c-d9f806731b45","service_instance_guid":"2c4605d1-f573-4dda-a1b9-2ba8c9b6f968","port":null,"domain_url":"/v2/shared_domains/fd41d865-0e43-4379-92c3-9f69a447987d","domain":{"metadata":{"guid":"fd41d865-0e43-4379-92c3-9f69a447987d","url":"/v2/shared_domains/fd41d865-0e43-4379-92c3-9f69a447987d","created_at":"2017-06-07T16:46:32Z","updated_at":"2017-07-18T02:26:02Z"},"entity":{"name":"cfapps.pie-ci-1-11.cfplatformeng.com","router_group_guid":null,"router_group_type":null}},"space_url":"/v2/spaces/780d8010-2c8b-4b0c-999c-d9f806731b45","space":{"metadata":{"guid":"780d8010-2c8b-4b0c-999c-d9f806731b45","url":"/v2/spaces/780d8010-2c8b-4b0c-999c-d9f806731b45","created_at":"2017-06-14T15:47:45Z","updated_at":"2017-06-14T15:47:45Z"},"entity":{"name":"dev","organization_guid":"15fa6d39-97ce-48ac-be70-db0e86a9387a","space_quota_definition_guid":null,"isolation_segment_guid":null,"allow_ssh":true,"organization_url":"/v2/organizations/15fa6d39-97ce-48ac-be70-db0e86a9387a","developers_url":"/v2/spaces/780d8010-2c8b-4b0c-999c-d9f806731b45/developers","managers_url":"/v2/spaces/780d8010-2c8b-4b0c-999c-d9f806731b45/managers","auditors_url":"/v2/spaces/780d8010-2c8b-4b0c-999c-d9f806731b45/auditors","apps_url":"/v2/spaces/780d8010-2c8b-4b0c-999c-d9f806731b45/apps","routes_url":"/v2/spaces/780d8010-2c8b-4b0c-999c-d9f806731b45/routes","domains_url":"/v2/spaces/780d8010-2c8b-4b0c-999c-d9f806731b45/domains","service_instances_url":"/v2/spaces/780d8010-2c8b-4b0c-999c-d9f806731b45/service_instances","app_events_url":"/v2/spaces/780d8010-2c8b-4b0c-999c-d9f806731b45/app_events","events_url":"/v2/spaces/780d8010-2c8b-4b0c-999c-d9f806731b45/events","security_groups_url":"/v2/spaces/780d8010-2c8b-4b0c-999c-d9f806731b45/security_groups","staging_security_groups_url":"/v2/spaces/780d8010-2c8b-4b0c-999c-d9f806731b45/staging_security_groups"}},"service_instance_url":"/v2/user_provided_service_instances/2c4605d1-f573-4dda-a1b9-2ba8c9b6f968","service_instance":{"metadata":{"guid":"2c4605d1-f573-4dda-a1b9-2ba8c9b6f968","url":"/v2/user_provided_service_instances/2c4605d1-f573-4dda-a1b9-2ba8c9b6f968","created_at":"2017-06-15T15:35:56Z","updated_at":"2017-06-15T15:35:56Z"},"entity":{"name":"my-route-service","credentials":{},"space_guid":"780d8010-2c8b-4b0c-999c-d9f806731b45","type":"user_provided_service_instance","syslog_drain_url":"","route_service_url":"https://kchen-route-service.cfapps.pie-ci-1-11.cfplatformeng.com","space_url":"/v2/spaces/780d8010-2c8b-4b0c-999c-d9f806731b45","service_bindings_url":"/v2/user_provided_service_instances/2c4605d1-f573-4dda-a1b9-2ba8c9b6f968/service_bindings","service_keys_url":"/v2/user_provided_service_instances/2c4605d1-f573-4dda-a1b9-2ba8c9b6f968/service_keys","routes_url":"/v2/user_provided_service_instances/2c4605d1-f573-4dda-a1b9-2ba8c9b6f968/routes"}},"apps_url":"/v2/routes/6873bf52-2976-426a-a326-c473bed29e96/apps","route_mappings_url":"/v2/routes/6873bf52-2976-426a-a326-c473bed29e96/route_mappings"}}],"events_url":"/v2/apps/913339ce-e25f-4193-8665-bfaf2d77970a/events","service_bindings_url":"/v2/apps/913339ce-e25f-4193-8665-bfaf2d77970a/service_bindings","service_bindings":[],"route_mappings_url":"/v2/apps/913339ce-e25f-4193-8665-bfaf2d77970a/route_mappings"}}]}`

	http.HandleFunc("/v2/apps", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, apps)
	})

	return http.ListenAndServe(":9911", nil)
}
