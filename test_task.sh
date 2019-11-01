#!/bin/bash
echo "==========================================="
echo
echo "I AM ${__K8S_LEADER_ELECTOR_ROLE}"
echo "MY IDENTITY IS ${__K8S_LEADER_ELECTOR_MY_IDENTITY}"
echo "LEADER IS ${__K8S_LEADER_ELECTOR_LEADER_IDENTITY}"
echo "ELECTED AT ${__K8S_LEADER_ELECTOR_ACQUIRE_TIME_RFC3339}"
echo "NUM OF LEADER TRANSITION IS ${__K8S_LEADER_ELECTOR_LEADER_TRANSITIONS}"
echo "RENEW TIME IS ${__K8S_LEADER_ELECTOR_RENEW_TIME_RFC3339}"
echo
echo "==========================================="

sleep 3

