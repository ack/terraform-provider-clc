package terraform_clc

import (
	"encoding/json"
	"fmt"
	"strconv"

	clc "github.com/CenturyLinkCloud/clc-sdk"
	"github.com/CenturyLinkCloud/clc-sdk/server"

	"github.com/hashicorp/terraform/helper/schema"
)

func resourceCLCPublicIP() *schema.Resource {
	return &schema.Resource{
		Create: resourceCLCPublicIPCreate,
		Read:   resourceCLCPublicIPRead,
		Update: resourceCLCPublicIPUpdate,
		Delete: resourceCLCPublicIPDelete,
		Schema: map[string]*schema.Schema{
			"server_id": &schema.Schema{
				Type:     schema.TypeString,
				Required: true,
			},
			"internal_ip_address": &schema.Schema{
				Type:     schema.TypeString,
				Optional: true,
				Computed: true,
				Default:  nil,
			},
			"ports": &schema.Schema{
				Type:     schema.TypeList,
				Required: true,
				Elem:     &schema.Schema{Type: schema.TypeMap},
			},
			"source_restrictions": &schema.Schema{
				Type:     schema.TypeList,
				Optional: true,
				Elem:     &schema.Schema{Type: schema.TypeMap},
			},
		},
	}
}

func resourceCLCPublicIPCreate(d *schema.ResourceData, meta interface{}) error {
	client := meta.(*clc.Client)
	sid := d.Get("server_id").(string)
	priv := d.Get("internal_ip_address").(string)
	ports, sources := parseIPSpec(d)
	req := server.PublicIP{
		Ports:              *ports,
		SourceRestrictions: *sources,
	}

	// since the API doesn't tell us the public IP it allocated,
	// track what was added after the call.
	ips := make(map[string]string)
	prev, err := client.Server.Get(sid)
	if err != nil {
		return fmt.Errorf("Failed finding server %v: %v", sid, err)
	}
	for _, i := range prev.Details.IPaddresses {
		ips[i.Internal] = i.Public
	}

	a, _ := json.Marshal(ips)
	LOG.Println(string(a))

	if priv != "" {
		// use existing private ip
		if _, present := ips[priv]; !present {
			return fmt.Errorf("Failed finding internal ip to use %v", priv)
		}
		req.InternalIP = priv
	}
	// execute the request
	resp, err := client.Server.AddPublicIP(sid, req)
	if err != nil {
		return fmt.Errorf("Failed reserving public ip: %v", err)
	}
	b, _ := json.Marshal(resp)
	LOG.Println(string(b))

	waitStatus(client, resp.ID)

	server, err := client.Server.Get(sid)
	if err != nil {
		return fmt.Errorf("Failed refreshing server for public ip: %v", err)
	}
	for _, i := range server.Details.IPaddresses {
		if priv != "" && i.Internal == priv {
			// bind
			LOG.Printf("Public IP bound on existing internal:%v - %v", i.Internal, i.Public)
			d.SetId(i.Public)
			break
		} else if ips[i.Internal] == "" && i.Public != "" {
			// allocate
			LOG.Printf("Public IP allocated on new internal:%v - %v", i.Internal, i.Public)
			d.SetId(i.Public)
			break
		}
	}
	return resourceCLCPublicIPRead(d, meta)
}

func resourceCLCPublicIPRead(d *schema.ResourceData, meta interface{}) error {
	client := meta.(*clc.Client)
	public_ip := d.Id()
	s := d.Get("server_id").(string)
	resp, err := client.Server.GetPublicIP(s, public_ip)
	if err != nil {
		LOG.Printf("Failed finding public ip: %v. Marking destroyed", d.Id())
		d.SetId("")
		return nil
	}

	d.Set("internal_ip_address", resp.InternalIP)
	d.Set("ports", resp.Ports)
	d.Set("source_restrictions", resp.SourceRestrictions)
	return nil
}

func resourceCLCPublicIPUpdate(d *schema.ResourceData, meta interface{}) error {
	client := meta.(*clc.Client)
	ip := d.Id()
	sid := d.Get("server_id").(string)
	if d.HasChange("ports") || d.HasChange("source_restrictions") {
		ports, sources := parseIPSpec(d)
		req := server.PublicIP{
			Ports:              *ports,
			SourceRestrictions: *sources,
		}
		resp, err := client.Server.UpdatePublicIP(sid, ip, req)
		if err != nil {
			return fmt.Errorf("Failed updating public ip: %v", err)
		}
		b, _ := json.Marshal(resp)
		LOG.Println(string(b))

		waitStatus(client, resp.ID)
		LOG.Printf("Successfully updated %v with %v", ip, req)
	}
	return nil
}

func resourceCLCPublicIPDelete(d *schema.ResourceData, meta interface{}) error {
	client := meta.(*clc.Client)
	s := d.Get("server_id").(string)
	ip := d.Id()
	LOG.Printf("Deleting public ip %v", ip)
	resp, err := client.Server.DeletePublicIP(s, ip)
	if err != nil {
		return fmt.Errorf("Failed deleting public ip: %v", err)
	}
	waitStatus(client, resp.ID)
	fmt.Printf("Public IP sucessfully deleted: %v", ip)
	return nil
}

func parseIPSpec(d *schema.ResourceData) (*[]server.Port, *[]server.SourceRestriction) {
	ports := make([]server.Port, 0)
	sources := make([]server.SourceRestriction, 0)
	if v := d.Get("ports"); v != nil {
		for _, v := range v.([]interface{}) {
			m := v.(map[string]interface{})
			p := server.Port{}
			port, err := strconv.Atoi(m["port"].(string))
			if err != nil {
				LOG.Printf("Failed parsing port '%v'. skipping", m["port"])
			}
			p.Protocol = m["protocol"].(string)
			p.Port = port
			through := -1
			if to := m["port_to"]; to != nil {
				through, _ = strconv.Atoi(to.(string))
				LOG.Printf("port range: %v-%v", port, through)
				p.PortTo = through
			}
			ports = append(ports, p)
		}
	}
	if v := d.Get("source_restrictions"); v != nil {
		for _, v := range v.([]interface{}) {
			m := v.(map[string]interface{})
			r := server.SourceRestriction{}
			r.CIDR = m["cidr"].(string)
			sources = append(sources, r)
		}
	}
	return &ports, &sources
}
