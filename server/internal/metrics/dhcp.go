package metrics

// DHCPDiscover increments the DISCOVER counter for scopeID.
func (r *Registry) DHCPDiscover(scopeID string) { r.dhcpDiscover.WithLabelValues(scopeID).Inc() }

// DHCPOffer increments the OFFER counter for scopeID.
func (r *Registry) DHCPOffer(scopeID string) { r.dhcpOffer.WithLabelValues(scopeID).Inc() }

// DHCPRequest increments the REQUEST counter for scopeID.
func (r *Registry) DHCPRequest(scopeID string) { r.dhcpRequest.WithLabelValues(scopeID).Inc() }

// DHCPAck increments the ACK counter for scopeID.
func (r *Registry) DHCPAck(scopeID string) { r.dhcpAck.WithLabelValues(scopeID).Inc() }

// DHCPNak increments the NAK counter for scopeID.
func (r *Registry) DHCPNak(scopeID string) { r.dhcpNak.WithLabelValues(scopeID).Inc() }

// DHCPDecline increments the DECLINE counter for scopeID.
func (r *Registry) DHCPDecline(scopeID string) { r.dhcpDecline.WithLabelValues(scopeID).Inc() }

// DHCPRelease increments the RELEASE counter for scopeID.
func (r *Registry) DHCPRelease(scopeID string) { r.dhcpRelease.WithLabelValues(scopeID).Inc() }

// DHCPNoLeases increments the pool-exhausted counter for scopeID.
func (r *Registry) DHCPNoLeases(scopeID string) { r.dhcpNoLeases.WithLabelValues(scopeID).Inc() }
