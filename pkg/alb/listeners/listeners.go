package listeners

import (
	"github.com/aws/aws-sdk-go/service/elbv2"

	extensions "k8s.io/api/extensions/v1beta1"

	listenerP "github.com/coreos/alb-ingress-controller/pkg/alb/listener"
	ruleP "github.com/coreos/alb-ingress-controller/pkg/alb/rule"
	rulesP "github.com/coreos/alb-ingress-controller/pkg/alb/rules"
	"github.com/coreos/alb-ingress-controller/pkg/alb/targetgroups"
	"github.com/coreos/alb-ingress-controller/pkg/annotations"
	albelbv2 "github.com/coreos/alb-ingress-controller/pkg/aws/elbv2"
	"github.com/coreos/alb-ingress-controller/pkg/util/log"
)

// Listeners is a slice of Listener pointers
type Listeners []*listenerP.Listener

// Find returns the position of the listener, returning -1 if unfound.
func (ls Listeners) Find(listener *elbv2.Listener) int {
	for p, v := range ls {
		if !v.NeedsModification(listener) {
			return p
		}
	}
	return -1
}

// Reconcile kicks off the state synchronization for every Listener in this Listeners instances.
// TODO: function has changed a lot, test
func (ls Listeners) Reconcile(rOpts *ReconcileOptions) (Listeners, error) {
	output := ls
	if len(ls) < 1 {
		return nil, nil
	}

	for _, listener := range ls {
		lOpts := listenerP.NewReconcileOptions()
		lOpts.SetEventf(rOpts.Eventf)
		lOpts.SetLoadBalancerArn(rOpts.LoadBalancerArn)
		lOpts.SetTargetGroups(rOpts.TargetGroups)
		if err := listener.Reconcile(lOpts); err != nil {
			return nil, err
		}

		rulesOpts := rulesP.NewReconcileOptions()
		rulesOpts.SetEventf(rOpts.Eventf)
		rulesOpts.SetListenerArn(listener.CurrentListener.ListenerArn)
		rulesOpts.SetTargetGroups(rOpts.TargetGroups)
		if rules, err := listener.Rules.Reconcile(rulesOpts); err != nil {
			return nil, err
		} else {
			listener.Rules = rules
		}
		if !listener.Deleted {
			output = append(output, listener)
		}
	}

	return output, nil
}

// StripDesiredState removes the DesiredListener from all Listeners in the slice.
func (ls Listeners) StripDesiredState() {
	for _, listener := range ls {
		listener.DesiredListener = nil
	}
}

// StripCurrentState takes all listeners and sets their CurrentListener to nil. Most commonly used
// when an ELB must be re-created fully. When the deletion of the ELB occurs, the listeners attached
// are also deleted, thus the ingress controller must know they no longer exist.
//
// Additionally, since Rules are also removed its Listener is, this also calles StripDesiredState on
// the Rules attached to each listener.
func (ls Listeners) StripCurrentState() {
	for _, listener := range ls {
		listener.CurrentListener = nil
		listener.Rules.StripCurrentState()
	}
}

// NewListenersFromAWSListeners returns a new listeners.Listeners based on an elbv2.Listeners.
func NewListenersFromAWSListeners(listeners []*elbv2.Listener, logger *log.Logger) (Listeners, error) {
	var output Listeners

	for _, listener := range listeners {
		logger.Infof("Fetching Rules for Listener %s", *listener.ListenerArn)
		rules, err := albelbv2.ELBV2svc.DescribeRules(&elbv2.DescribeRulesInput{ListenerArn: listener.ListenerArn})
		if err != nil {
			return nil, err
		}

		l := listenerP.NewListenerFromAWSListener(listener, logger)

		for _, rule := range rules.Rules {
			logger.Debugf("Assembling rule for: %s", log.Prettify(rule.Conditions))
			r := ruleP.NewRuleFromAWSRule(rule, logger)

			l.Rules = append(l.Rules, r)
		}

		output = append(output, l)
	}
	return output, nil
}

type NewListenersFromIngressOptions struct {
	Ingress     *extensions.Ingress
	Listeners   *Listeners
	Annotations *annotations.Annotations
	Logger      *log.Logger
	Priority    int
}

func NewListenersFromIngress(o *NewListenersFromIngressOptions) (Listeners, error) {
	var err error
	output := *o.Listeners
	var priority int

	for _, rule := range o.Ingress.Spec.Rules {
		// Listeners are constructed based on path and port.
		// Start with a new listener
		listenerList := listenerP.NewListener(o.Annotations, o.Logger)
		hostname := rule.Host

		for _, listener := range listenerList {
			// If this listener is already defined, copy the desired state over
			if i := output.Find(listener.DesiredListener); i >= 0 {
				output[i].DesiredListener = listener.DesiredListener
				listener = output[i]
			} else {
				output = append(output, listener)
			}

			listener.Rules, priority, err = rulesP.NewRulesFromIngress(&rulesP.NewRulesFromIngressOptions{
				Hostname:      hostname,
				Logger:        o.Logger,
				ListenerRules: &listener.Rules,
				Rule:          &rule,
				Priority:      priority,
			})
			if err != nil {
				return nil, err
			}

		}
	}

	return output, nil
}

type ReconcileOptions struct {
	Eventf          func(string, string, string, ...interface{})
	LoadBalancerArn *string
	Listeners       *Listeners
	TargetGroups    *targetgroups.TargetGroups
}

func NewReconcileOptions() *ReconcileOptions {
	return &ReconcileOptions{}
}

func (r *ReconcileOptions) SetLoadBalancerArn(s *string) *ReconcileOptions {
	r.LoadBalancerArn = s
	return r
}

func (r *ReconcileOptions) SetListeners(s *Listeners) *ReconcileOptions {
	r.Listeners = s
	return r
}

func (r *ReconcileOptions) SetEventf(f func(string, string, string, ...interface{})) *ReconcileOptions {
	r.Eventf = f
	return r
}

func (r *ReconcileOptions) SetTargetGroups(targetgroups *targetgroups.TargetGroups) *ReconcileOptions {
	r.TargetGroups = targetgroups
	return r
}